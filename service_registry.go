package nakamapluskit

import (
	"context"
	"sync"
	"time"

	"github.com/doublemo/nakama-plus-kit/core/etcd"
	grpcpool "github.com/doublemo/nakama-plus-kit/core/grpc-pool"
	"github.com/heroiclabs/nakama-common/rtapi"
	"github.com/heroiclabs/nakama-common/runtime"
	"go.uber.org/zap"
	"google.golang.org/protobuf/encoding/protojson"
)

type ServiceRegistry struct {
	ctx                  context.Context
	ctxCancelFn          context.CancelFunc
	logger               *zap.Logger
	services             map[string]map[string]runtime.PeerService
	meta                 *rtapi.NakamaPeer_NodeMeta
	protojsonMarshaler   *protojson.MarshalOptions
	protojsonUnmarshaler *protojson.UnmarshalOptions
	stoped               chan struct{}
	config               *Configuration
	once                 sync.Once
	sync.RWMutex
}

func NewServiceRegistry(ctx context.Context, logger *zap.Logger, meta *rtapi.NakamaPeer_NodeMeta, protojsonMarshaler *protojson.MarshalOptions, protojsonUnmarshaler *protojson.UnmarshalOptions, config *Configuration) *ServiceRegistry {
	ctx, cancel := context.WithCancel(ctx)
	s := &ServiceRegistry{
		ctx:                  ctx,
		ctxCancelFn:          cancel,
		logger:               logger,
		services:             make(map[string]map[string]runtime.PeerService),
		meta:                 meta,
		protojsonMarshaler:   protojsonMarshaler,
		protojsonUnmarshaler: protojsonUnmarshaler,
		stoped:               make(chan struct{}),
		config:               config,
	}
	return s
}

func (s *ServiceRegistry) GetServicesByRole(role string) []runtime.PeerService {
	s.RLock()
	defer s.RUnlock()
	m, ok := s.services[role]
	if !ok {
		return make([]runtime.PeerService, 0)
	}
	services := make([]runtime.PeerService, len(m))
	k := 0
	for _, v := range m {
		services[k] = v
		k++
	}
	return services
}

func (s *ServiceRegistry) GetServicesWithNakama() []runtime.PeerService {
	return s.GetServicesByRole("nakama")
}

func (s *ServiceRegistry) Shutdown(timeout time.Duration) {
	s.once.Do(func() {
		if s.ctxCancelFn == nil {
			return
		}

		s.ctxCancelFn()
		timer := time.NewTicker(timeout)
		defer timer.Stop()

		select {
		case <-s.stoped:
		case <-timer.C:
		}
	})
}

func (s *ServiceRegistry) Start(client *etcd.ClientV3, updateChan chan struct{}) {
	s.update(client, updateChan)
	go func() {
		defer func() {
			if err := client.Deregister(s.meta.Name); err != nil {
				s.logger.Error("Failed to shutdown Deregister", zap.Error(err))
			}

			close(s.stoped)
			s.logger.Info("服务已停止", zap.String("name", s.meta.Name))
		}()

		if err := client.Register(s.meta.Name, s.marshalMetadata()); err != nil {
			s.logger.Error("Failed to register service", zap.Error(err))
			return
		}

		ch := make(chan struct{}, 1)
		go client.Watch(ch)
		s.logger.Info("服务已启动", zap.String("name", s.meta.Name))
		for {
			select {
			case <-ch:
				s.update(client, updateChan)

			case <-s.ctx.Done():
				return
			}
		}
	}()
}

func (s *ServiceRegistry) marshalMetadata() string {
	metadata, _ := s.protojsonMarshaler.Marshal(s.meta)
	return string(metadata)
}

func (s *ServiceRegistry) unmarshalMetadata(metadata []byte) (*rtapi.NakamaPeer_NodeMeta, error) {
	var md rtapi.NakamaPeer_NodeMeta
	if err := s.protojsonUnmarshaler.Unmarshal(metadata, &md); err != nil {
		return nil, err
	}
	return &md, nil
}

func (s *ServiceRegistry) update(client *etcd.ClientV3, updateChan chan struct{}) {
	entries, err := client.GetEntries()
	if err != nil {
		s.logger.Error("Failed to GetEntries", zap.Error(err))
		return
	}

	nodes := make(map[string]map[string]runtime.PeerService)
	num := 0
	roleNum := 0
	for _, v := range entries {
		nodemeta, err := s.unmarshalMetadata([]byte(v))
		if err != nil {
			s.logger.Error("Failed to unmarshalMetadata", zap.Error(err))
			continue
		}

		service := NewService(s.ctx, s.logger, s.meta.Name, s.meta.Role, nodemeta, &grpcpool.Options{
			MaxIdle:              s.config.Grpc.GrpcPoolMaxIdle,
			MaxActive:            s.config.Grpc.GrpcPoolMaxActive,
			MaxConcurrentStreams: s.config.Grpc.GrpcPoolMaxConcurrentStreams,
			Reuse:                s.config.Grpc.GrpcPoolReuse,
		})

		num++
		m, ok := nodes[nodemeta.Role]
		if !ok {
			roleNum++
			nodes[nodemeta.Role] = make(map[string]runtime.PeerService)
			nodes[nodemeta.Role][nodemeta.Name] = service
			continue
		}
		m[nodemeta.Name] = service
	}

	s.Lock()
	oldServices := s.services
	s.services = nodes
	s.Unlock()

	for k, v := range oldServices {
		if k == "nakama" {
			continue
		}

		for _, vv := range v {
			vv.Close()
		}
	}

	s.logger.Debug("Successfully updated service", zap.Int("num", num), zap.Int("role_number", roleNum))
	if updateChan == nil {
		return
	}

	select {
	case updateChan <- struct{}{}:
	default:
	}
}
