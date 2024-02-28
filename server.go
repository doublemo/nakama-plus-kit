package nakamapluskit

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/doublemo/nakama-plus-kit/core/etcd"
	pool "github.com/doublemo/nakama-plus-kit/core/grpc-pool"
	"github.com/doublemo/nakama-plus-kit/core/math"
	grpc_prometheus "github.com/grpc-ecosystem/go-grpc-prometheus"
	"github.com/heroiclabs/nakama-common/rtapi"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/channelz/service"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
)

type Server struct {
	rtapi.NakamaPeerApiServer
	name            string
	role            string
	ctx             context.Context
	ctxCancelFn     context.CancelFunc
	logger          *zap.Logger
	handler         atomic.Value
	grpc            *grpc.Server
	serviceRegistry *ServiceRegistry
	sessionRegistry *SessionRegistry
	config          *Configuration
	once            sync.Once
}

func NewServer(ctx context.Context, logger *zap.Logger, protojsonMarshaler *protojson.MarshalOptions, protojsonUnmarshaler *protojson.UnmarshalOptions, md map[string]string, c *Configuration) *Server {
	ctx, cancel := context.WithCancel(ctx)
	meta := rtapi.NakamaPeer_NodeMeta{
		Name: c.Name,
		Role: c.Role,
		Vars: md,
	}

	s := &Server{
		name:            c.Name,
		role:            c.Role,
		ctx:             ctx,
		ctxCancelFn:     cancel,
		logger:          logger,
		config:          c,
		sessionRegistry: NewSessionRegistry(),
	}

	opts := []grpc.ServerOption{
		grpc.InitialWindowSize(pool.InitialWindowSize),
		grpc.InitialConnWindowSize(pool.InitialConnWindowSize),
		grpc.MaxSendMsgSize(pool.MaxSendMsgSize),
		grpc.MaxRecvMsgSize(pool.MaxRecvMsgSize),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			PermitWithoutStream: true,
		}),
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    pool.KeepAliveTime,
			Timeout: pool.KeepAliveTimeout,
		}),
	}

	if len(c.Grpc.GrpcX509Key) > 0 && len(c.Grpc.GrpcX509Pem) > 0 {
		cert, err := tls.LoadX509KeyPair(c.Grpc.GrpcX509Pem, c.Grpc.GrpcX509Key)
		if err != nil {
			logger.Fatal("Failed load x509", zap.Error(err))
		}

		opts = append(opts,
			grpc.ChainStreamInterceptor(ensureStreamValidToken(c), grpc_prometheus.StreamServerInterceptor),
			grpc.ChainUnaryInterceptor(ensureValidToken(c), grpc_prometheus.UnaryServerInterceptor),
			grpc.Creds(credentials.NewServerTLSFromCert(&cert)),
		)
	}

	var (
		listen net.Listener
		err    error
	)

	port := c.Grpc.Port
	for i := 0; i < 1000; i++ {
		if c.Grpc.Port == 0 {
			port = math.RandomBetween(c.Grpc.MinPort, c.Grpc.MaxPort)
		}

		listen, err = net.Listen("tcp", net.JoinHostPort(c.Grpc.Addr, strconv.Itoa(port)))
		if err != nil {
			logger.Warn("Failed listen from addr", zap.Error(err), zap.String("addr", c.Grpc.Addr), zap.Int("port", port))
			continue
		}

		break
	}

	if listen == nil {
		logger.Fatal("Failed listen from addr", zap.Error(err), zap.String("addr", c.Grpc.Addr), zap.Int("port", c.Grpc.Port))
	}

	meta.Ip = c.Grpc.Addr
	meta.Port = uint32(port)
	if meta.Ip == "" || meta.Ip == "0.0.0.0" {
		meta.Ip = c.Grpc.Domain
	}
	s.grpc = grpc.NewServer(opts...)
	rtapi.RegisterNakamaPeerApiServer(s.grpc, s)
	s.serviceRegistry = NewServiceRegistry(ctx, logger, &meta, protojsonMarshaler, protojsonUnmarshaler, c)
	service.RegisterChannelzServiceToServer(s.grpc)
	grpc_prometheus.Register(s.grpc)
	healthpb.RegisterHealthServer(s.grpc, health.NewServer())
	go func() {
		logger.Info("Starting API server for gRPC requests", zap.Int("port", port))
		if err := s.grpc.Serve(listen); err != nil {
			logger.Fatal("API server listener failed", zap.Error(err))
		}
	}()
	return s
}

func (s *Server) Call(ctx context.Context, in *rtapi.NakamaPeer_Envelope) (*rtapi.NakamaPeer_Envelope, error) {
	fmt.Println("收到消息了 call")
	if h, ok := s.handler.Load().(Handler); ok && h != nil {
		fmt.Println("收到消息了w call")
		return h.Call(ctx, in)
	}
	return nil, status.Error(codes.Internal, "Missing handler")
}

func (s *Server) Stream(stream rtapi.NakamaPeerApi_StreamServer) error {
	md, ok := metadata.FromIncomingContext(stream.Context())
	if !ok || (len(md["node"]) == 0 || len(md["role"]) == 0) {
		return status.Error(codes.InvalidArgument, "Missing metadata")
	}

	node := md["node"][0]
	role := md["role"][0]
	handler, ok := s.handler.Load().(Handler)
	if !ok || handler == nil {
		return status.Error(codes.Internal, "Missing handler")
	}

	session := NewSession(s.ctx, s.logger, node, role, stream, s.config.Grpc.GrpcPoolMessageQueueSize, handler)
	s.sessionRegistry.Add(session)
	session.Consume()
	s.sessionRegistry.Remove(session.ID())
	return nil
}

func (s *Server) Start(etcd *etcd.ClientV3, h Handler, updated func(serviceRegistry *ServiceRegistry)) {
	s.handler.Store(h)
	ch := make(chan struct{}, 1)
	s.serviceRegistry.Start(etcd, ch)

	go func() {
		for {
			select {
			case <-ch:
				updated(s.serviceRegistry)

			case <-s.ctx.Done():
				return
			}
		}
	}()
}

func (s *Server) Stop() {
	s.once.Do(func() {
		if s.ctxCancelFn == nil {
			return
		}

		if s.serviceRegistry != nil {
			s.serviceRegistry.Shutdown(time.Second * 10)
		}

		s.grpc.GracefulStop()
		s.ctxCancelFn()
	})
}

func ensureValidToken(config *Configuration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (resp interface{}, err error) {
		md, ok := metadata.FromIncomingContext(ctx)
		if !ok {
			return nil, status.Error(codes.InvalidArgument, "Missing metadata")
		}
		// The keys within metadata.MD are normalized to lowercase.
		// See: https://godoc.org/google.golang.org/grpc/metadata#New
		authorization := md["authorization"]
		if len(authorization) < 1 {
			return nil, status.Error(codes.PermissionDenied, "Invalid token")
		}

		token := strings.TrimPrefix(authorization[0], "Bearer ")
		if token != config.Grpc.GrpcToken {
			return nil, status.Error(codes.PermissionDenied, "Invalid token")
		}

		// Continue execution of handler after ensuring a valid token.
		return handler(ctx, req)
	}
}

// func(srv interface{}, ss ServerStream, info *StreamServerInfo, handler StreamHandler)
func ensureStreamValidToken(config *Configuration) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		md, ok := metadata.FromIncomingContext(ss.Context())
		if !ok {
			return status.Error(codes.PermissionDenied, "Invalid token")
		}

		authorization := md["authorization"]
		if len(authorization) < 1 {
			return status.Error(codes.PermissionDenied, "Invalid token")
		}

		token := strings.TrimPrefix(authorization[0], "Bearer ")
		if token != config.Grpc.GrpcToken {
			return status.Error(codes.PermissionDenied, "Invalid token")
		}

		// Continue execution of handler after ensuring a valid token.
		return handler(srv, ss)
	}
}
