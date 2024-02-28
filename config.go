package nakamapluskit

import (
	"github.com/doublemo/nakama-plus-kit/core/etcd"
	"go.uber.org/zap"
)

type GrpcConfiguration struct {
	Addr                         string `yaml:"addr" json:"addr" usage:"服务监听地址"`
	Port                         int    `yaml:"port" json:"port" usage:"服务监听端口, 默认为0."`
	MinPort                      int    `yaml:"min-port" json:"minPort" usage:"当端口为0时,将使用最小端口和最大端口进行随机"`
	MaxPort                      int    `yaml:"max-port" json:"maxPort" usage:"当端口为0时,将使用最小端口和最大端口进行随机"`
	Domain                       string `yaml:"domain" json:"domain" usage:"本机服务地址"`
	GrpcX509Pem                  string `yaml:"grpc_x509_pem" json:"grpc_x509_pem" usage:"ssl pem"`
	GrpcX509Key                  string `yaml:"grpc_x509_key" json:"grpc_x509_key" usage:"ssl key"`
	GrpcToken                    string `yaml:"grpc_token" json:"grpc_token" usage:"token"`
	GrpcPoolMaxIdle              int    `yaml:"grpc_pool_max_idle" json:"grpc_pool_max_idle" usage:"Maximum number of idle connections in the grpc pool"`
	GrpcPoolMaxActive            int    `yaml:"grpc_pool_max_active" json:"grpc_pool_max_active" usage:"Maximum number of connections allocated by the grpc pool at a given time."`
	GrpcPoolMaxConcurrentStreams int    `yaml:"grpc_pool_max_concurrent_streams" json:"grpc_pool_max_concurrent_streams" usage:"MaxConcurrentStreams limit on the number of concurrent grpc streams to each single connection,create a one-time connection to return."`
	GrpcPoolReuse                bool   `yaml:"grpc_pool_reuse" json:"grpc_pool_reuse" usage:"If Reuse is true and the pool is at the GrpcPoolMaxActive limit, then Get() reuse,the connection to return, If Reuse is false and the pool is at the MaxActive limit"`
	GrpcPoolMessageQueueSize     int    `yaml:"grpc_pool_message_queue_size" json:"grpc_pool_message_queue_size" usage:"grpc message queue size"`
}

func (c *GrpcConfiguration) Check(logger *zap.Logger) {
	if c.Port == 0 {
		if c.MinPort > c.MaxPort || c.MaxPort < 1 {
			logger.Fatal("服务端口监听失败, MinPort或MaxPort无效")
		}
	}
}

func NewGrpcConfiguration() *GrpcConfiguration {
	return &GrpcConfiguration{
		MinPort:                      19000,
		MaxPort:                      39000,
		GrpcPoolMaxIdle:              1,
		GrpcPoolMaxActive:            64,
		GrpcPoolMaxConcurrentStreams: 64,
		GrpcPoolReuse:                true,
		GrpcPoolMessageQueueSize:     128,
	}
}

type Configuration struct {
	Name string               `yaml:"name" json:"name" usage:"服务名称"`
	Role string               `yaml:"role" json:"role" usage:"服务角色"`
	Grpc *GrpcConfiguration   `yaml:"grpc" json:"grpc" usage:"grpc设置"`
	Etcd *etcd.Clientv3Config `yaml:"etcd" json:"etcd" usage:"etcd设置"`
}

func (c *Configuration) Check(log *zap.Logger) error {
	c.Grpc.Check(log)
	return nil
}

func NewConfiguration(name, role string) *Configuration {
	return &Configuration{
		Name: name,
		Role: role,
		Grpc: NewGrpcConfiguration(),
		Etcd: etcd.NewClientv3Config(),
	}
}
