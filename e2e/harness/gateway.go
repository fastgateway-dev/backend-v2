//go:build e2e

package harness

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// Gateway makes data-plane requests through the Envoy Gateway
// LoadBalancer, mirroring regression/helpers/gateway.py's GatewayClient.
type Gateway struct {
	cfg *Config
}

// NewGateway returns a Gateway bound to cfg.
func NewGateway(cfg *Config) *Gateway {
	return &Gateway{cfg: cfg}
}

// tlsConfig builds the TLS config gateway.py achieves via
// `verify=False` + `extensions={"sni_hostname": self.domain.encode()}`:
// dial the IP directly, but present the configured domain as SNI, and
// skip certificate verification since the test certs are self-signed.
func (g *Gateway) tlsConfig(clientCertPEM, clientKeyPEM string) (*tls.Config, error) {
	cfg := &tls.Config{
		InsecureSkipVerify: true, // test certs are self-signed
		ServerName:         g.cfg.GatewayDomain,
	}
	if clientCertPEM != "" {
		cert, err := tls.X509KeyPair([]byte(clientCertPEM), []byte(clientKeyPEM))
		if err != nil {
			return nil, fmt.Errorf("load client certificate: %w", err)
		}
		cfg.Certificates = []tls.Certificate{cert}
	}
	return cfg, nil
}

// --- HTTP ---

// Response is the result of an HTTP request made through Gateway.HTTP.
type Response struct {
	StatusCode int
	Header     http.Header
	Body       []byte
}

// JSON decodes the response body into out.
func (r *Response) JSON(out any) error {
	return json.Unmarshal(r.Body, out)
}

type reqOptions struct {
	headers         http.Header
	body            []byte
	clientCertPEM   string
	clientKeyPEM    string
	followRedirects bool
	timeout         time.Duration
}

// ReqOpt configures a single Gateway.HTTP call.
type ReqOpt func(*reqOptions)

// WithHeader adds a request header. May be called multiple times.
func WithHeader(key, value string) ReqOpt {
	return func(o *reqOptions) { o.headers.Add(key, value) }
}

// WithBody sets the raw request body.
func WithBody(b []byte) ReqOpt {
	return func(o *reqOptions) { o.body = b }
}

// WithClientCert supplies a PEM-encoded client certificate/key pair for
// mTLS requests.
func WithClientCert(certPEM, keyPEM string) ReqOpt {
	return func(o *reqOptions) { o.clientCertPEM = certPEM; o.clientKeyPEM = keyPEM }
}

// WithFollowRedirects controls whether the client follows HTTP redirects
// (default: false, matching gateway.py's follow_redirects=False default).
func WithFollowRedirects(follow bool) ReqOpt {
	return func(o *reqOptions) { o.followRedirects = follow }
}

// WithTimeout overrides the default 10s request timeout.
func WithTimeout(d time.Duration) ReqOpt {
	return func(o *reqOptions) { o.timeout = d }
}

// HTTP makes an HTTP request through the gateway: it dials cfg.GatewayIP
// directly but sets both the Host header and the TLS SNI to
// cfg.GatewayDomain, exactly as gateway.py's http_request does.
func (g *Gateway) HTTP(ctx context.Context, method, path string, opts ...ReqOpt) (*Response, error) {
	o := &reqOptions{headers: http.Header{}, timeout: 10 * time.Second}
	for _, opt := range opts {
		opt(o)
	}

	tlsCfg, err := g.tlsConfig(o.clientCertPEM, o.clientKeyPEM)
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Transport: &http.Transport{TLSClientConfig: tlsCfg},
		Timeout:   o.timeout,
	}
	if !o.followRedirects {
		client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		}
	}

	target := fmt.Sprintf("https://%s:%d%s", g.cfg.GatewayIP, g.cfg.GatewayPort, path)
	var bodyReader io.Reader
	if o.body != nil {
		bodyReader = bytes.NewReader(o.body)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, bodyReader)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Host = g.cfg.GatewayDomain // forces the Host header, like gateway.py's req_headers["Host"]
	for k, vs := range o.headers {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("%s %s: read response body: %w", method, path, err)
	}

	return &Response{StatusCode: resp.StatusCode, Header: resp.Header, Body: respBody}, nil
}

// --- gRPC ---

// rawCodecName is the gRPC content-subtype under which rawCodec is
// registered. The gateway.py predecessor shelled out to grpcurl and
// string-matched stderr; here we dial with a real grpc.ClientConn and
// invoke generically so the harness needs no generated stub per proto.
//
// How this works: the client sends "content-type: application/grpc+raw".
// The upstream server (a normal generated grpc-go service, running in a
// different process) does not have "raw" registered, so per grpc-go's
// Server.getCodec it falls back to the standard proto codec -- meaning
// the bytes callers pass via WithGRPCMessage must already be valid
// protobuf wire-format for the target request message. rawCodec itself
// never interprets the bytes; it only makes ClientConn.Invoke's generic
// (args, reply any) signature work over []byte instead of proto.Message.
const rawCodecName = "raw"

type rawCodec struct{}

func (rawCodec) Marshal(v any) ([]byte, error) {
	b, ok := v.([]byte)
	if !ok {
		return nil, fmt.Errorf("rawCodec: expected []byte, got %T", v)
	}
	return b, nil
}

func (rawCodec) Unmarshal(data []byte, v any) error {
	p, ok := v.(*[]byte)
	if !ok {
		return fmt.Errorf("rawCodec: expected *[]byte, got %T", v)
	}
	*p = append([]byte(nil), data...)
	return nil
}

func (rawCodec) Name() string { return rawCodecName }

func init() {
	encoding.RegisterCodec(rawCodec{})
}

// GRPCResult is the result of a Gateway.GRPC call. Tests assert on Code
// directly (codes.ResourceExhausted, codes.PermissionDenied,
// codes.Unauthenticated, ...) instead of substring-matching CLI stderr.
type GRPCResult struct {
	Code     codes.Code
	Message  string
	Header   metadata.MD
	Trailer  metadata.MD
	Response []byte
}

type grpcOptions struct {
	message []byte
	md      metadata.MD
	timeout time.Duration
}

// GRPCOpt configures a single Gateway.GRPC call.
type GRPCOpt func(*grpcOptions)

// WithGRPCMessage sets the raw protobuf-encoded request payload.
func WithGRPCMessage(b []byte) GRPCOpt {
	return func(o *grpcOptions) { o.message = b }
}

// WithGRPCMetadata adds an outgoing metadata entry (e.g. an API key or
// bearer token header). May be called multiple times.
func WithGRPCMetadata(key, value string) GRPCOpt {
	return func(o *grpcOptions) {
		if o.md == nil {
			o.md = metadata.MD{}
		}
		o.md.Append(key, value)
	}
}

// WithGRPCTimeout overrides the default 10s call timeout.
func WithGRPCTimeout(d time.Duration) GRPCOpt {
	return func(o *grpcOptions) { o.timeout = d }
}

// dialGRPC opens a ClientConn to the gateway (cfg.GatewayIP:cfg.GatewayPort,
// TLS SNI/:authority forced to cfg.GatewayDomain), shared by GRPC and
// GRPCTyped. Callers must Close() the returned conn.
func (g *Gateway) dialGRPC() (*grpc.ClientConn, error) {
	tlsCfg, err := g.tlsConfig("", "")
	if err != nil {
		return nil, err
	}

	target := fmt.Sprintf("%s:%d", g.cfg.GatewayIP, g.cfg.GatewayPort)
	conn, err := grpc.NewClient(target,
		grpc.WithTransportCredentials(credentials.NewTLS(tlsCfg)),
		grpc.WithAuthority(g.cfg.GatewayDomain),
	)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", target, err)
	}
	return conn, nil
}

// GRPC invokes service/method through the gateway using a real
// google.golang.org/grpc client (dialed at cfg.GatewayIP:cfg.GatewayPort
// with TLS SNI/:authority forced to cfg.GatewayDomain), and returns the
// typed status code rather than parsed CLI output.
//
// The request/response are raw protobuf-encoded bytes (see
// WithGRPCMessage): callers hand-encode the wire format themselves, and
// GRPCResult.Response comes back as undecoded bytes too. This exists for
// reflection-based or dynamic calls where no generated stub is available;
// when a stub exists (see e2e/testdata/pb), prefer GRPCTyped.
func (g *Gateway) GRPC(ctx context.Context, service, method string, opts ...GRPCOpt) (*GRPCResult, error) {
	o := &grpcOptions{timeout: 10 * time.Second}
	for _, opt := range opts {
		opt(o)
	}

	conn, err := g.dialGRPC()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	callCtx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	if len(o.md) > 0 {
		callCtx = metadata.NewOutgoingContext(callCtx, o.md)
	}

	fullMethod := fmt.Sprintf("/%s/%s", service, method)
	var header, trailer metadata.MD
	var respBytes []byte
	invokeErr := conn.Invoke(callCtx, fullMethod, o.message, &respBytes,
		grpc.CallContentSubtype(rawCodecName),
		grpc.Header(&header),
		grpc.Trailer(&trailer),
	)

	st, _ := status.FromError(invokeErr)
	return &GRPCResult{
		Code:     st.Code(),
		Message:  st.Message(),
		Header:   header,
		Trailer:  trailer,
		Response: respBytes,
	}, nil
}

// GRPCTyped invokes service/method through the gateway exactly like GRPC,
// but using generated protobuf stubs (see e2e/testdata/pb and the `protos`
// Makefile target) instead of hand-encoded bytes: req is marshaled with
// the standard proto codec, and the response is unmarshaled directly into
// resp (a non-nil pointer to a generated message type, e.g. &echo.Message{}).
//
// GRPCResult.Response is left nil since the decoded payload is already in
// resp; Code/Message/Header/Trailer behave exactly as they do for GRPC.
func (g *Gateway) GRPCTyped(ctx context.Context, service, method string, req, resp proto.Message, opts ...GRPCOpt) (*GRPCResult, error) {
	o := &grpcOptions{timeout: 10 * time.Second}
	for _, opt := range opts {
		opt(o)
	}

	conn, err := g.dialGRPC()
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	callCtx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	if len(o.md) > 0 {
		callCtx = metadata.NewOutgoingContext(callCtx, o.md)
	}

	fullMethod := fmt.Sprintf("/%s/%s", service, method)
	var header, trailer metadata.MD
	invokeErr := conn.Invoke(callCtx, fullMethod, req, resp,
		grpc.Header(&header),
		grpc.Trailer(&trailer),
	)

	st, _ := status.FromError(invokeErr)
	return &GRPCResult{
		Code:    st.Code(),
		Message: st.Message(),
		Header:  header,
		Trailer: trailer,
	}, nil
}
