package math

import (
	"crypto/rand"
	"math/big"
	"time"
)

func Salt() *big.Int {
	max := new(big.Int)
	max.Exp(big.NewInt(2), big.NewInt(130), nil).Sub(max, big.NewInt(1))
	n, err := rand.Int(rand.Reader, max)
	if err != nil {
		return big.NewInt(0).SetInt64(time.Now().UnixNano())
	}
	return n
}
