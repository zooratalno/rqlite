package command

import (
	"bytes"
	"compress/gzip"
	"expvar"
	"fmt"
	"io/ioutil"

	"google.golang.org/protobuf/proto"
)

const (
	defaultBatchThreshold = 5
	defaultSizeThreshold  = 150
)

// Requester is the interface objects must support to be marshaled
// successfully.
type Requester interface {
	proto.Message
	GetRequest() *Request
}

// RequestMarshaler marshals Request objects, potentially performing
// gzip compression.
type RequestMarshaler struct {
	BatchThreshold   int
	SizeThreshold    int
	ForceCompression bool
}

const (
	numRequests             = "num_requests"
	numCompressedRequests   = "num_compressed_requests"
	numUncompressedRequests = "num_uncompressed_requests"
	numCompressedBytes      = "num_compressed_bytes"
	numPrecompressedBytes   = "num_precompressed_bytes"
	numUncompressedBytes    = "num_uncompressed_bytes"
	numCompressionMisses    = "num_compression_misses"
)

// stats captures stats for the Proto marshaler.
var stats *expvar.Map

func init() {
	stats = expvar.NewMap("proto")
	stats.Add(numRequests, 0)
	stats.Add(numCompressedRequests, 0)
	stats.Add(numUncompressedRequests, 0)
	stats.Add(numCompressedBytes, 0)
	stats.Add(numUncompressedBytes, 0)
	stats.Add(numCompressionMisses, 0)
	stats.Add(numPrecompressedBytes, 0)
}

// NewRequestMarshaler returns an initialized RequestMarshaler.
func NewRequestMarshaler() *RequestMarshaler {
	return &RequestMarshaler{
		BatchThreshold: defaultBatchThreshold,
		SizeThreshold:  defaultSizeThreshold,
	}
}

// Marshal marshals a Requester object, returning a byte slice, a bool
// indicating whether the contents are compressed, or an error.
func (m *RequestMarshaler) Marshal(r Requester) ([]byte, bool, error) {
	stats.Add(numRequests, 1)
	compress := false

	stmts := r.GetRequest().GetStatements()
	if len(stmts) >= m.BatchThreshold {
		compress = true
	} else {
		for i := range stmts {
			if len(stmts[i].Sql) >= m.SizeThreshold {
				compress = true
				break
			}
		}
	}

	b, err := proto.Marshal(r)
	if err != nil {
		return nil, false, err
	}
	ubz := len(b)
	stats.Add(numPrecompressedBytes, int64(ubz))

	if compress {
		// Let's try compression.
		gzData, err := gzCompress(b)
		if err != nil {
			return nil, false, err
		}

		// Is compression better?
		if ubz > len(gzData) || m.ForceCompression {
			// Yes! Let's keep it.
			b = gzData
			stats.Add(numCompressedRequests, 1)
			stats.Add(numCompressedBytes, int64(len(b)))
		} else {
			// No. :-( Dump it.
			compress = false
			stats.Add(numCompressionMisses, 1)
		}
	} else {
		stats.Add(numUncompressedRequests, 1)
		stats.Add(numUncompressedBytes, int64(len(b)))
	}

	return b, compress, nil
}

// Stats returns status and diagnostic information about
// the RequestMarshaler.
func (m *RequestMarshaler) Stats() map[string]interface{} {
	return map[string]interface{}{
		"compression_size":  m.SizeThreshold,
		"compression_batch": m.BatchThreshold,
		"force_compression": m.ForceCompression,
	}
}

// Marshal marshals a Command.
func Marshal(c *Command) ([]byte, error) {
	return proto.Marshal(c)
}

// Unmarshal unmarshals a Command
func Unmarshal(b []byte, c *Command) error {
	return proto.Unmarshal(b, c)
}

// MarshalNoop marshals a Noop command
func MarshalNoop(c *Noop) ([]byte, error) {
	return proto.Marshal(c)
}

// UnmarshalNoop unmarshals a Noop command
func UnmarshalNoop(b []byte, c *Noop) error {
	return proto.Unmarshal(b, c)
}

// MarshalLoadRequest marshals a LoadRequest command
func MarshalLoadRequest(lr *LoadRequest) ([]byte, error) {
	b, err := proto.Marshal(lr)
	if err != nil {
		return nil, err
	}
	return gzCompress(b)
}

// UnmarshalLoadRequest unmarshals a LoadRequest command
func UnmarshalLoadRequest(b []byte, lr *LoadRequest) error {
	u, err := gzUncompress(b)
	if err != nil {
		return err
	}
	return proto.Unmarshal(u, lr)
}

// UnmarshalSubCommand unmarshalls a sub command m. It assumes that
// m is the correct type.
func UnmarshalSubCommand(c *Command, m proto.Message) error {
	b := c.SubCommand
	if c.Compressed {
		var err error
		b, err = gzUncompress(b)
		if err != nil {
			return fmt.Errorf("unmarshal sub uncompress: %s", err)
		}
	}

	if err := proto.Unmarshal(b, m); err != nil {
		return fmt.Errorf("proto unmarshal: %s", err)
	}
	return nil
}

// gzCompress compresses the given byte slice.
func gzCompress(b []byte) ([]byte, error) {
	var buf bytes.Buffer
	gzw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, fmt.Errorf("gzip new writer: %s", err)
	}

	if _, err := gzw.Write(b); err != nil {
		return nil, fmt.Errorf("gzip Write: %s", err)
	}
	if err := gzw.Close(); err != nil {
		return nil, fmt.Errorf("gzip Close: %s", err)
	}
	return buf.Bytes(), nil
}

func gzUncompress(b []byte) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("unmarshal gzip NewReader: %s", err)
	}

	ub, err := ioutil.ReadAll(gz)
	if err != nil {
		return nil, fmt.Errorf("unmarshal gzip ReadAll: %s", err)
	}

	if err := gz.Close(); err != nil {
		return nil, fmt.Errorf("unmarshal gzip Close: %s", err)
	}
	return ub, nil
}

func MapConsistencyLevel(in QueryRequest_Level) ExecuteQueryRequest_Level {
	switch in {
	case QueryRequest_QUERY_REQUEST_LEVEL_NONE:
		return ExecuteQueryRequest_QUERY_REQUEST_LEVEL_NONE
	case QueryRequest_QUERY_REQUEST_LEVEL_WEAK:
		return ExecuteQueryRequest_QUERY_REQUEST_LEVEL_WEAK
	case QueryRequest_QUERY_REQUEST_LEVEL_STRONG:
		return ExecuteQueryRequest_QUERY_REQUEST_LEVEL_STRONG
	default:
		return ExecuteQueryRequest_QUERY_REQUEST_LEVEL_WEAK
	}
}
