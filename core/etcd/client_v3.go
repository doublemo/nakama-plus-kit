package etcd

import (
	"context"
	"crypto/tls"
	"strings"
	"sync"
	"time"

	clientv3 "go.etcd.io/etcd/client/v3"
	"go.etcd.io/etcd/pkg/transport"
	"go.uber.org/zap"
)

type (
	Clientv3Config struct {
		Endpoints              []string `yaml:"endpoints" json:"endpoints" usage:"Endpoints is a list of URLs."`
		AutoSyncIntervalMs     int      `yaml:"auto-sync-interval-ms" json:"autoSyncIntervalMs" usage:"AutoSyncInterval is the interval to update endpoints with its latest members.0 disables auto-sync. By default auto-sync is disabled."`
		DialTimeoutMs          int      `yaml:"dial-timeout-ms" json:"dialTimeoutMs" usage:"DialTimeout is the timeout for failing to establish a connection."`
		DialKeepAliveTimeMs    int      `yaml:"dial-keep-alive-time-ms" json:"dialKeepAliveTimeMs" usage:"DialKeepAliveTime is the time after which client pings the server to see if transport is alive."`
		DialKeepAliveTimeoutMs int      `yaml:"dial-keep-alive-timeout-ms" json:"dialKeepAliveTimeoutMs" usage:"DialKeepAliveTimeout is the time that the client waits for a response for the keep-alive probe. If the response is not received in this time, the connection is closed."`
		MaxCallSendMsgSize     int      `yaml:"max-call-send-msg-size" json:"maxCallSendMsgSize" usage:"MaxCallSendMsgSize is the client-side request send limit in bytes.If 0, it defaults to 2.0 MiB (2 * 1024 * 1024)."`
		MaxCallRecvMsgSize     int      `yaml:"max-call-recv-msg-size" json:"maxCallRecvMsgSize" usage:"MaxCallRecvMsgSize is the client-side response receive limit.If 0, it defaults to math.MaxInt32, because range response can easily exceed request send limits."`
		Username               string   `yaml:"username" json:"username" usage:"Username is a user name for authentication."`
		Password               string   `yaml:"password" json:"password" usage:"Password is a password for authentication."`
		Cert                   string   `yaml:"cert" json:"cert" usage:"Cert"`
		Key                    string   `yaml:"key" json:"key" usage:"Key"`
		CACert                 string   `yaml:"ca-cert" json:"caCert" usage:"CACert"`
		ServicePrefix          string   `yaml:"service-prefix" json:"servicePrefix" usage:"Service prefix"`
	}

	ClientV3 struct {
		ctx           context.Context
		ctxCancelFn   context.CancelFunc
		logger        *zap.Logger
		kv            clientv3.KV
		client        *clientv3.Client
		watcher       clientv3.Watcher
		leaseID       clientv3.LeaseID
		hbch          <-chan *clientv3.LeaseKeepAliveResponse
		leaser        clientv3.Lease
		servicePrefix string
		once          sync.Once
	}
)

func (c *Clientv3Config) Check(logger *zap.Logger) {
	if size := len(c.Endpoints); size < 1 {
		logger.Fatal("无法获取到etcd地址")
	}

	if c.ServicePrefix == "" {
		c.ServicePrefix = "/nakama-plus/peer/services/"
	}
}

func NewClientv3Config() *Clientv3Config {
	return &Clientv3Config{
		ServicePrefix: "/nakama-plus/peer/services/",
	}
}

func NewClientV3(ctx context.Context, logger *zap.Logger, c *Clientv3Config) *ClientV3 {
	ctx, cancel := context.WithCancel(ctx)
	s := &ClientV3{
		ctx:           ctx,
		ctxCancelFn:   cancel,
		logger:        logger,
		servicePrefix: c.ServicePrefix,
	}

	cfg, err := bindPeerEtcdV3Config2V3Config(c)
	if err != nil {
		logger.Fatal("Failed to build etcd config", zap.Error(err))
	}

	client, err := clientv3.New(*cfg)
	if err != nil {
		logger.Fatal("Failed to connect etcd", zap.Error(err))
	}

	s.kv = clientv3.NewKV(client)
	s.watcher = clientv3.NewWatcher(client)
	s.leaser = clientv3.NewLease(client)
	s.client = client
	return s
}

func (c *ClientV3) GetEntries() ([]string, error) {
	resp, err := c.kv.Get(c.ctx, c.servicePrefix, clientv3.WithPrefix())
	if err != nil {
		return nil, err
	}

	entries := make([]string, len(resp.Kvs))
	for i, kv := range resp.Kvs {
		entries[i] = string(kv.Value)
	}

	return entries, nil
}

func (c *ClientV3) ServicePrefix() string {
	return c.servicePrefix
}

func (c *ClientV3) Watch(ch chan struct{}) {
	wch := c.watcher.Watch(c.ctx, c.servicePrefix, clientv3.WithPrefix(), clientv3.WithRev(0))
	ch <- struct{}{}
	for wr := range wch {
		if wr.Canceled {
			return
		}
		ch <- struct{}{}
	}
}

func (c *ClientV3) Update(name, value string) error {
	if _, err := c.client.Put(c.ctx, c.Key(name), value, clientv3.WithLease(c.leaseID)); err != nil {
		return err
	}
	return nil
}

func (c *ClientV3) Register(name, value string) error {
	grantResp, err := c.leaser.Grant(c.ctx, 180)
	if err != nil {
		return err
	}
	c.leaseID = grantResp.ID
	_, err = c.kv.Put(c.ctx, c.Key(name), value, clientv3.WithLease(c.leaseID))
	if err != nil {
		return err
	}

	c.hbch, err = c.leaser.KeepAlive(c.ctx, c.leaseID)
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case r := <-c.hbch:
				// avoid dead loop when channel was closed
				if r == nil {
					return
				}
			case <-c.ctx.Done():
				return
			}
		}
	}()
	return nil
}

func (c *ClientV3) Deregister(name string) error {
	defer c.close()
	if _, err := c.client.Delete(c.ctx, c.Key(name), clientv3.WithIgnoreLease()); err != nil {
		return err
	}
	return nil
}

func (c *ClientV3) close() {
	c.once.Do(func() {
		if c.leaser != nil {
			c.leaser.Close()
		}

		if c.watcher != nil {
			c.watcher.Close()
		}

		if c.ctxCancelFn != nil {
			c.ctxCancelFn()
		}
	})
}

func (c *ClientV3) Key(name string) string {
	key := c.servicePrefix
	if !strings.HasSuffix(key, "/") {
		key += "/"
	}
	key += name
	return key
}

func bindPeerEtcdV3Config2V3Config(c *Clientv3Config) (*clientv3.Config, error) {
	var (
		tlscfg *tls.Config
		err    error
	)
	if c.Cert != "" && c.Key != "" {
		tlsInfo := transport.TLSInfo{
			CertFile:      c.Cert,
			KeyFile:       c.Key,
			TrustedCAFile: c.CACert,
		}
		tlscfg, err = tlsInfo.ClientConfig()
		if err != nil {
			return nil, err
		}
	}
	cfg := &clientv3.Config{
		Endpoints:         c.Endpoints,
		DialTimeout:       time.Second * 3,
		DialKeepAliveTime: time.Second * 3,
		TLS:               tlscfg,
		Username:          c.Username,
		Password:          c.Password,
	}

	if c.AutoSyncIntervalMs > 0 {
		cfg.AutoSyncInterval = time.Duration(c.AutoSyncIntervalMs) * time.Millisecond
	}

	if c.DialTimeoutMs > 0 {
		cfg.DialTimeout = time.Duration(c.DialTimeoutMs) * time.Millisecond
	}

	if c.DialKeepAliveTimeMs > 0 {
		cfg.DialKeepAliveTime = time.Duration(c.DialKeepAliveTimeMs) * time.Millisecond
	}

	if c.DialKeepAliveTimeoutMs > 0 {
		cfg.DialKeepAliveTimeout = time.Duration(c.DialKeepAliveTimeoutMs) * time.Millisecond
	}

	if c.MaxCallSendMsgSize > 0 {
		cfg.MaxCallSendMsgSize = c.MaxCallSendMsgSize
	}

	if c.MaxCallRecvMsgSize > 0 {
		cfg.MaxCallRecvMsgSize = c.MaxCallRecvMsgSize
	}
	return cfg, nil
}
