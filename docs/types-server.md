# Types Server

The types server is a lightweight HTTP sidecar that decodes raw hex-encoded instruction data into human-readable JSON. It runs alongside your extension and provides a `/decode` endpoint that frontends, dashboards, and debugging tools can call to display instruction payloads and results in structured form — without needing to know the Go types themselves.

## Why It Exists

When instructions flow through the TEE pipeline, the `data` field in messages and results is raw bytes (hex-encoded). To display these in a UI or inspect them during development, something needs to know the structure — which Go type to unmarshal into, and whether the data is a message (request) or a result (response).

The types server solves this by maintaining a registry of decoders keyed by `(OPType, OPCommand, Kind)`. You register your types once, and any tool can call `/decode` to get structured JSON back.

## Architecture

```
pkg/decoder/
├── decoder.go      Decoder interface + DataKind type
├── registry.go     Thread-safe Registry (Register, Lookup, Keys)
├── json.go         JSONDecoder[T] — decodes JSON-encoded bytes
└── abi.go          ABIDecoder[T] — decodes ABI-encoded bytes

pkg/types/
├── types.go        Your request/response/state structs
└── register.go     RegisterDecoders() — wires types into the registry

internal/typesserver/
└── server.go       HTTP server (POST /decode, GET /registry, GET /health)

cmd/types-server/
└── main.go         Standalone entry point

pkg/server/
└── typesserver.go  StartTypesServer() helper for embedded use
```

## How It Runs

The types server runs in two modes:

**Embedded (default)** — started automatically inside the Docker container alongside your extension. The `cmd/docker/main.go` entry point calls `StartTypesServer()` in a goroutine:

```go
go extserver.StartTypesServer(config.TypesServerPort)
```

**Standalone** — for local development or running the types server independently:

```bash
TYPES_SERVER_PORT=8100 go run ./cmd/types-server
```

The port is configured via the `TYPES_SERVER_PORT` environment variable (default: `8100`).

## API

### `POST /decode`

Decodes hex-encoded data into structured JSON.

**Request:**

```json
{
  "opType": "GREETING",
  "opCommand": "SAY_HELLO",
  "kind": "message",
  "data": "0x7b226e616d65223a22416c696365227d"
}
```

| Field | Type | Description |
|-------|------|-------------|
| `opType` | string | Operation type (e.g. `"GREETING"`) |
| `opCommand` | string | Operation command (e.g. `"SAY_HELLO"`, falls back to `""`) |
| `kind` | string | `"message"` for requests, `"result"` for responses |
| `data` | string | Hex-encoded bytes (with `0x` prefix) |

**Success response (200):**

```json
{
  "decoded": {
    "name": "Alice"
  }
}
```

**Error responses:**

| Status | When |
|--------|------|
| 400 | Invalid request body, invalid `kind`, or invalid hex data |
| 404 | No decoder registered for the `(opType, opCommand, kind)` combination |
| 422 | Decoder found but data could not be decoded |

### `GET /registry`

Returns all registered decoder keys.

**Response (200):**

```json
[
  { "opType": "GREETING", "opCommand": "SAY_HELLO", "kind": "message" },
  { "opType": "GREETING", "opCommand": "SAY_HELLO", "kind": "result" },
  { "opType": "GREETING", "opCommand": "SAY_GOODBYE", "kind": "message" },
  { "opType": "GREETING", "opCommand": "SAY_GOODBYE", "kind": "result" }
]
```

### `GET /health`

**Response (200):**

```json
{
  "status": "ok",
  "version": "0.1.0"
}
```

## Testing the Types Server

### Local smoke test

Start the server and hit each endpoint:

```bash
# Start the types server
TYPES_SERVER_PORT=8100 go run ./cmd/types-server &

# Health check
curl -s http://localhost:8100/health | jq .

# List registered decoders
curl -s http://localhost:8100/registry | jq .

# Decode a GREETING/SAY_HELLO message
# The hex below is {"name":"Alice"} encoded as 0x-prefixed hex
curl -s -X POST http://localhost:8100/decode \
  -H 'Content-Type: application/json' \
  -d '{
    "opType": "GREETING",
    "opCommand": "SAY_HELLO",
    "kind": "message",
    "data": "0x7b226e616d65223a22416c696365227d"
  }' | jq .

# Decode a GREETING/SAY_HELLO result
# The hex below is {"greeting":"Hello, Alice!","greetingNumber":1}
curl -s -X POST http://localhost:8100/decode \
  -H 'Content-Type: application/json' \
  -d '{
    "opType": "GREETING",
    "opCommand": "SAY_HELLO",
    "kind": "result",
    "data": "0x7b2267726565744e756d626572223a312c2267726565746696e67223a2248656c6c6f2c20416c69636521227d"
  }' | jq .

# Stop the background server
kill %1
```

### Generating hex test data

To convert any JSON string to hex for the `data` field:

```bash
echo -n '{"name":"Alice"}' | xxd -p | tr -d '\n' | sed 's/^/0x/'
# Output: 0x7b226e616d65223a22416c696365227d
```

### Error cases to verify

```bash
# Unknown op type → 404
curl -s -X POST http://localhost:8100/decode \
  -d '{"opType":"UNKNOWN","opCommand":"SAY_HELLO","kind":"message","data":"0x7b7d"}' | jq .

# Invalid kind → 400
curl -s -X POST http://localhost:8100/decode \
  -d '{"opType":"GREETING","opCommand":"SAY_HELLO","kind":"invalid","data":"0x7b7d"}' | jq .

# Invalid hex → 400
curl -s -X POST http://localhost:8100/decode \
  -d '{"opType":"GREETING","opCommand":"SAY_HELLO","kind":"message","data":"not-hex"}' | jq .

# Valid hex but invalid JSON for the type → 422
curl -s -X POST http://localhost:8100/decode \
  -d '{"opType":"GREETING","opCommand":"SAY_HELLO","kind":"message","data":"0xdeadbeef"}' | jq .
```

### Docker test

When running via Docker Compose, the types server is available on port 8100 inside the container:

```bash
docker compose up -d --build
curl -s http://localhost:8100/health | jq .
curl -s http://localhost:8100/registry | jq .
```

> **Note:** To expose the types server port from Docker, add a `ports` mapping to `docker-compose.yaml`:
> ```yaml
> extension-tee:
>   ports:
>     - "8100:8100"
> ```

## Adding Your Own Types

When you add a new operation type to your extension, you also register decoders so the types server can decode its payloads. There are three steps:

### 1. Define your types in `pkg/types/types.go`

Add your request and response structs alongside the existing ones:

```go
// PlaceOrderRequest is sent via the Solidity contract.
type PlaceOrderRequest struct {
    Symbol string  `json:"symbol"`
    Amount float64 `json:"amount"`
    Price  float64 `json:"price"`
}

// PlaceOrderResponse is returned in ActionResult.Data.
type PlaceOrderResponse struct {
    OrderID string `json:"orderId"`
    Status  string `json:"status"`
}
```

### 2. Register decoders in `pkg/types/register.go`

Add registrations for each `(OPType, OPCommand, Kind)` combination:

```go
func RegisterDecoders(r *decoder.Registry) {
    // Existing GREETING decoders
    r.Register(
        decoder.RegistryKey{OPType: "GREETING", OPCommand: "SAY_HELLO", Kind: decoder.KindMessage},
        decoder.NewJSONDecoder[SayHelloRequest](),
    )
    r.Register(
        decoder.RegistryKey{OPType: "GREETING", OPCommand: "SAY_HELLO", Kind: decoder.KindResult},
        decoder.NewJSONDecoder[SayHelloResponse](),
    )
    r.Register(
        decoder.RegistryKey{OPType: "GREETING", OPCommand: "SAY_GOODBYE", Kind: decoder.KindMessage},
        decoder.NewABIDecoder[SayGoodbyeRequest](SayGoodbyeMessageArg),
    )
    r.Register(
        decoder.RegistryKey{OPType: "GREETING", OPCommand: "SAY_GOODBYE", Kind: decoder.KindResult},
        decoder.NewJSONDecoder[SayGoodbyeResponse](),
    )

    // New PLACE_ORDER decoders
    r.Register(
        decoder.RegistryKey{OPType: "PLACE_ORDER", Kind: decoder.KindMessage},
        decoder.NewJSONDecoder[PlaceOrderRequest](),
    )
    r.Register(
        decoder.RegistryKey{OPType: "PLACE_ORDER", Kind: decoder.KindResult},
        decoder.NewJSONDecoder[PlaceOrderResponse](),
    )
}
```

### 3. Verify the registration

Rebuild and check:

```bash
go build ./... && go vet ./...
TYPES_SERVER_PORT=8100 go run ./cmd/types-server &
curl -s http://localhost:8100/registry | jq .
kill %1
```

You should see `GREETING` (SAY_HELLO and SAY_GOODBYE) and `PLACE_ORDER` entries.

## Choosing a Decoder

The `pkg/decoder` package provides two decoder implementations:

### `JSONDecoder[T]` — for JSON-encoded payloads

Use this when your Solidity contract passes a JSON string as the message bytes. This is the common case for most extensions.

```go
decoder.NewJSONDecoder[SayHelloRequest]()
```

### `ABIDecoder[T]` — for ABI-encoded payloads

Use this when your Solidity contract passes ABI-encoded data (e.g. `abi.encode(arg1, arg2)`). This uses `structs.Decode` from `go-flare-common` to decode the ABI bytes.

```go
import "github.com/ethereum/go-ethereum/accounts/abi"

arg := abi.Argument{Type: yourABIType}
decoder.NewABIDecoder[YourStruct](arg)
```

ABI decoding is useful when your contract encodes structured data more efficiently than JSON, or when you want type safety at the Solidity level.

## Using OPCommand for Sub-Operations

The `OPCommand` field allows a single `OPType` to have multiple decoders for different sub-operations. For example, the `GREETING` operation has different request formats for `SAY_HELLO` vs `SAY_GOODBYE`:

```go
r.Register(
    decoder.RegistryKey{OPType: "GREETING", OPCommand: "SAY_HELLO", Kind: decoder.KindMessage},
    decoder.NewJSONDecoder[SayHelloRequest](),
)
r.Register(
    decoder.RegistryKey{OPType: "GREETING", OPCommand: "SAY_GOODBYE", Kind: decoder.KindMessage},
    decoder.NewABIDecoder[SayGoodbyeRequest](SayGoodbyeMessageArg),
)
```

The registry's `Lookup` method tries an exact `(OPType, OPCommand, Kind)` match first, then falls back to `(OPType, "", Kind)`. This means you can register a default decoder with an empty `OPCommand` that handles requests when no specific sub-operation is specified.

## Writing a Custom Decoder

If you need decoding logic beyond JSON or ABI, implement the `Decoder` interface:

```go
type Decoder interface {
    Decode(data []byte) (any, error)
}
```

For example, a decoder that handles a custom binary format:

```go
type MyCustomDecoder struct{}

func (d *MyCustomDecoder) Decode(data []byte) (any, error) {
    // Your custom decoding logic here
    return parsed, nil
}
```

Then register it like any other decoder:

```go
r.Register(
    decoder.RegistryKey{OPType: "MY_OP", Kind: decoder.KindMessage},
    &MyCustomDecoder{},
)
```
