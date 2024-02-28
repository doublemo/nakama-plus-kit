package nakamapluskit

import (
	"context"
	"net"
	"strconv"
	"sync"
	"sync/atomic"

	grpcpool "github.com/doublemo/nakama-plus-kit/core/grpc-pool"
	"github.com/heroiclabs/nakama-common/rtapi"
	"github.com/heroiclabs/nakama-common/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc/metadata"
)

type (
	Service struct {
		ctx          context.Context
		ctxCancelFn  context.CancelFunc
		logger       *zap.Logger
		serverNode   string
		serverRole   string
		md           *rtapi.NakamaPeer_NodeMeta
		streamClient atomic.Value
		pool         atomic.Value
		poolOption   *grpcpool.Options
		once         sync.Once
	}
)

func NewService(ctx context.Context, logger *zap.Logger, serverNode, serverRole string, md *rtapi.NakamaPeer_NodeMeta, option *grpcpool.Options) runtime.PeerService {
	ctx, cancel := context.WithCancel(ctx)
	s := &Service{
		ctx:         ctx,
		ctxCancelFn: cancel,
		logger:      logger,
		serverNode:  serverNode,
		serverRole:  serverRole,
		md:          md,
	}
	return s
}

func (s *Service) Name() string {
	return s.md.Name
}

func (s *Service) Metadata() *rtapi.NakamaPeer_NodeMeta {
	md := &rtapi.NakamaPeer_NodeMeta{
		Name: s.md.Name,
		Ip:   s.md.Ip,
		Port: s.md.Port,
		Role: s.md.Role,
		Vars: make(map[string]string, len(s.md.Vars)),
	}

	for k, v := range s.md.Vars {
		md.Vars[k] = v
	}
	return md
}

func (s *Service) Do(msg *rtapi.NakamaPeer_Envelope) (*rtapi.NakamaPeer_Envelope, error) {
	md := make(map[string]string, len(msg.Context)+2)
	for k, v := range msg.Context {
		md[k] = v
	}
	md["node"] = s.serverNode
	md["role"] = s.serverRole

	pool, err := s.getPool()
	if err != nil {
		return nil, err
	}

	p, err := pool.Get()
	if err != nil {
		return nil, err
	}

	defer p.Close()
	client := rtapi.NewNakamaPeerApiClient(p.Value())
	return client.Call(s.ctx, msg)
}

func (s *Service) Send(msg *rtapi.NakamaPeer_Envelope) error {
	streamClient, err := s.getStreamClient()
	if err != nil {
		return err
	}

	return streamClient.Send(msg)
}

func (s *Service) Close() error {
	s.once.Do(func() {
		if s.ctxCancelFn == nil {
			return
		}

		select {
		case <-s.ctx.Done():
			return
		default:
		}

		s.ctxCancelFn()
		if stream, ok := s.streamClient.Load().(rtapi.NakamaPeerApi_StreamClient); ok && stream != nil {
			if err := stream.CloseSend(); err != nil {
				s.logger.Error("stream.CloseSend", zap.Error(err))
			}
		}

		if pool, ok := s.pool.Load().(grpcpool.Pool); ok && pool != nil {
			if err := pool.Close(); err != nil {
				s.logger.Error("pool.Close", zap.Error(err))
			}
		}
	})
	return nil
}

func (s *Service) Recv(fn func(service runtime.PeerService, msg *rtapi.NakamaPeer_Envelope)) error {
	stream, err := s.getStreamClient()
	if err != nil {
		return err
	}

	go func() {
		for {
			select {
			case <-s.ctx.Done():
				return
			default:
			}

			out, err := stream.Recv()
			if err != nil {
				s.logger.Warn("recv message error", zap.Error(err))
				return
			}

			fn(s, out)
		}
	}()
	return nil
}

func (s *Service) getPool() (grpcpool.Pool, error) {
	pool, ok := s.pool.Load().(grpcpool.Pool)
	if ok && pool != nil {
		return pool, nil
	}

	m, err := grpcpool.New(net.JoinHostPort(s.md.Ip, strconv.Itoa(int(s.md.Port))), *s.poolOption)
	if err != nil {
		return nil, err
	}
	s.pool.Store(m)
	return m, nil
}

func (s *Service) getStreamClient() (rtapi.NakamaPeerApi_StreamClient, error) {
	stream, ok := s.streamClient.Load().(rtapi.NakamaPeerApi_StreamClient)
	if ok && stream != nil {
		return stream, nil
	}

	pool, err := s.getPool()
	if err != nil {
		return nil, err
	}

	p, err := pool.Get()
	if err != nil {
		return nil, err
	}

	defer p.Close()

	apiClient := rtapi.NewNakamaPeerApiClient(p.Value())
	ctx := metadata.NewOutgoingContext(s.ctx, metadata.MD{
		"node": []string{s.serverNode},
		"role": []string{s.serverRole}},
	)

	streacmClient, err := apiClient.Stream(ctx)
	if err != nil {
		return nil, err
	}
	s.streamClient.Store(streacmClient)
	return streacmClient, nil
}
