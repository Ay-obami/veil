package decoder

import (
	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/flare-foundation/go-flare-common/pkg/tee/structs"
)

// ABIDecoder decodes ABI-encoded bytes into a value of type T.
type ABIDecoder[T any] struct {
	arg abi.Argument
}

// NewABIDecoder creates an ABIDecoder for the given ABI argument.
func NewABIDecoder[T any](arg abi.Argument) *ABIDecoder[T] {
	return &ABIDecoder[T]{arg: arg}
}

func (d *ABIDecoder[T]) Decode(data []byte) (any, error) {
	return structs.Decode[T](d.arg, data)
}
