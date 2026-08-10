package types

import "veil/pkg/decoder"

// RegisterDecoders registers all type decoders for the orderbook extension.
func RegisterDecoders(r *decoder.Registry) {
	// DEPOSIT message + result
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "DEPOSIT", Kind: decoder.KindMessage},
		decoder.NewJSONDecoder[DepositRequest](),
	)
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "DEPOSIT", Kind: decoder.KindResult},
		decoder.NewJSONDecoder[DepositResponse](),
	)

	// WITHDRAW message + result
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "WITHDRAW", Kind: decoder.KindMessage},
		decoder.NewJSONDecoder[WithdrawRequest](),
	)
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "WITHDRAW", Kind: decoder.KindResult},
		decoder.NewJSONDecoder[WithdrawResponse](),
	)

	// PLACE_ORDER message + result
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "PLACE_ORDER", Kind: decoder.KindMessage},
		decoder.NewJSONDecoder[PlaceOrderRequest](),
	)
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "PLACE_ORDER", Kind: decoder.KindResult},
		decoder.NewJSONDecoder[PlaceOrderResponse](),
	)

	// CANCEL_ORDER message + result
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "CANCEL_ORDER", Kind: decoder.KindMessage},
		decoder.NewJSONDecoder[CancelOrderRequest](),
	)
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "CANCEL_ORDER", Kind: decoder.KindResult},
		decoder.NewJSONDecoder[CancelOrderResponse](),
	)

	// GET_MY_STATE (request + result)
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "GET_MY_STATE", Kind: decoder.KindMessage},
		decoder.NewJSONDecoder[GetMyStateRequest](),
	)
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "GET_MY_STATE", Kind: decoder.KindResult},
		decoder.NewJSONDecoder[GetMyStateResponse](),
	)

	// GET_BOOK_STATE (request + result — response reuses StateResponse)
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "GET_BOOK_STATE", Kind: decoder.KindMessage},
		decoder.NewJSONDecoder[GetBookStateRequest](),
	)
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "GET_BOOK_STATE", Kind: decoder.KindResult},
		decoder.NewJSONDecoder[StateResponse](),
	)

	// GET_CANDLES (request + result)
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "GET_CANDLES", Kind: decoder.KindMessage},
		decoder.NewJSONDecoder[GetCandlesRequest](),
	)
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "GET_CANDLES", Kind: decoder.KindResult},
		decoder.NewJSONDecoder[GetCandlesResponse](),
	)

	// GET_SOLVENCY_COMMITMENT (request + result) — new for Veil, not in the base fce-orderbook.
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "GET_SOLVENCY_COMMITMENT", Kind: decoder.KindMessage},
		decoder.NewJSONDecoder[GetSolvencyCommitmentRequest](),
	)
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "GET_SOLVENCY_COMMITMENT", Kind: decoder.KindResult},
		decoder.NewJSONDecoder[GetSolvencyCommitmentResponse](),
	)

	// EXPORT_HISTORY message + result
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "EXPORT_HISTORY", Kind: decoder.KindMessage},
		decoder.NewJSONDecoder[ExportHistoryRequest](),
	)
	r.Register(
		decoder.RegistryKey{OPType: "ORDERBOOK", OPCommand: "EXPORT_HISTORY", Kind: decoder.KindResult},
		decoder.NewJSONDecoder[ExportHistoryResponse](),
	)
}
