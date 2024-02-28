package nakamapluskit

import (
	"context"

	"github.com/heroiclabs/nakama-common/rtapi"
	"go.uber.org/atomic"
	"go.uber.org/zap"
)

type (
	Session interface {
		ID() string
		Role() string
		Context() context.Context
		Consume()
	}

	LocalSession struct {
		ctx         context.Context
		ctxCancelFn context.CancelFunc
		id          string
		role        string
		conn        rtapi.NakamaPeerApi_StreamServer
		logger      *zap.Logger
		outgoingCh  chan *rtapi.NakamaPeer_Envelope
		stopped     *atomic.Bool
		handler     Handler
	}
)

func NewSession(ctx context.Context, logger *zap.Logger, id, role string, conn rtapi.NakamaPeerApi_StreamServer, outgoingQueueSize int, handler Handler) Session {
	sessionLogger := logger.With(zap.String("id", id))
	sessionLogger.Info("New session connected", zap.String("role", role))
	ctx, cancel := context.WithCancel(ctx)
	return &LocalSession{
		ctx:         ctx,
		ctxCancelFn: cancel,
		logger:      logger,
		id:          id,
		role:        role,
		conn:        conn,
		outgoingCh:  make(chan *rtapi.NakamaPeer_Envelope, outgoingQueueSize),
		stopped:     atomic.NewBool(false),
		handler:     handler,
	}
}

func (s *LocalSession) ID() string {
	return s.id
}

func (s *LocalSession) Role() string {
	return s.role
}

func (s *LocalSession) Context() context.Context {
	return s.ctx
}

func (s *LocalSession) Consume() {
	go s.processOutgoing()
IncomingLoop:
	for {
		select {
		case <-s.ctx.Done():
			break IncomingLoop

		default:
		}

		payload, err := s.conn.Recv()
		if err != nil {
			s.logger.Debug("Error reading message from client", zap.Error(err))
			break
		}
		s.handler.NotifyMsg(s, payload)
	}
	s.Close()
}

func (s *LocalSession) processOutgoing() {
OutgoingLoop:
	for {
		select {
		case <-s.ctx.Done():
			break OutgoingLoop

		case payload := <-s.outgoingCh:
			if ok := s.stopped.Load(); ok {
				break OutgoingLoop
			}

			if err := s.conn.Send(payload); err != nil {
				s.logger.Warn("Failed to set write deadline", zap.Error(err))
				break OutgoingLoop
			}
		}
	}
	s.Close()
}

func (s *LocalSession) Close() {
	s.logger.Info("Closed client connection", zap.String("id", s.id))
}
