package nakamapluskit

import (
	"context"

	"github.com/doublemo/nakama-plus-common/rtapi"
)

type (
	Handler interface {
		Call(ctx context.Context, in *rtapi.NakamaPeer_Envelope) (*rtapi.NakamaPeer_Envelope, error)
		NotifyMsg(session Session, in *rtapi.NakamaPeer_Envelope)
	}
)
