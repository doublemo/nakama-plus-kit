package nakamapluskit

import (
	"context"

	"github.com/heroiclabs/nakama-common/rtapi"
)

type (
	Handler interface {
		Call(ctx context.Context, in *rtapi.NakamaPeer_Envelope) (*rtapi.NakamaPeer_Envelope, error)
		NotifyMsg(session Session, in *rtapi.NakamaPeer_Envelope)
	}
)
