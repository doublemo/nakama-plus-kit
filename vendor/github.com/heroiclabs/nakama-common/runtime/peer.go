package runtime

import (
	"context"

	"github.com/heroiclabs/nakama-common/rtapi"
)

type (
	PeerService interface {
		Name() string
		Do(msg *rtapi.NakamaPeer_Envelope) (*rtapi.NakamaPeer_Envelope, error)
		Send(msg *rtapi.NakamaPeer_Envelope) error
		Close() error
		Metadata() *rtapi.NakamaPeer_NodeMeta
	}

	Peer interface {
		Send(node string, msg *rtapi.NakamaPeer_Frame, reliable bool) error
		Broadcast(msg *rtapi.NakamaPeer_Frame, reliable, includeSelf bool)
		Call(ctx context.Context, node string, msg *rtapi.NakamaPeer_Frame) (*rtapi.NakamaPeer_Frame, error)
		SessionToken(tk string) (userID, username string, vars map[string]string, exp int64, online bool, err error)
		GetServicesByRole(role string) []PeerService
		GetServicesByRoleAndName(role, name string) (PeerService, bool)
	}
)
