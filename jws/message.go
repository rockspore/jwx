package jws

import (
	"context"
	"strings"

	"github.com/lestrrat-go/jwx/internal/base64"
	"github.com/lestrrat-go/jwx/internal/json"
	"github.com/lestrrat-go/jwx/internal/pool"
	"github.com/lestrrat-go/jwx/jwk"
	"github.com/pkg/errors"
)

func NewSignature() *Signature {
	return &Signature{}
}

func (s Signature) PublicHeaders() Headers {
	return s.headers
}

func (s *Signature) SetPublicHeaders(v Headers) *Signature {
	s.headers = v
	return s
}

func (s Signature) ProtectedHeaders() Headers {
	return s.protected
}

func (s *Signature) SetProtectedHeaders(v Headers) *Signature {
	s.protected = v
	return s
}

func (s Signature) Signature() []byte {
	return s.signature
}

func (s *Signature) SetSignature(v []byte) *Signature {
	s.signature = v
	return s
}

// Sign populates the signature field, with a signature generated by
// given the signer object and payload.
//
// The first return value is the raw signature in binary format.
// The second return value s the full three-segment signature
// (e.g. "eyXXXX.XXXXX.XXXX")
func (s *Signature) Sign(payload []byte, signer Signer, key interface{}) ([]byte, []byte, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	hdrs, err := mergeHeaders(ctx, s.headers, s.protected)
	if err != nil {
		return nil, nil, errors.Wrap(err, `failed to merge headers`)
	}

	if err := hdrs.Set(AlgorithmKey, signer.Algorithm()); err != nil {
		return nil, nil, errors.Wrap(err, `failed to set "alg"`)
	}

	// If the key is a jwk.Key instance, obtain the raw key
	if jwkKey, ok := key.(jwk.Key); ok {
		// If we have a key ID specified by this jwk.Key, use that in the header
		if kid := jwkKey.KeyID(); kid != "" {
			if err := hdrs.Set(jwk.KeyIDKey, kid); err != nil {
				return nil, nil, errors.Wrap(err, `set key ID from jwk.Key`)
			}
		}
	}
	hdrbuf, err := json.Marshal(hdrs)
	if err != nil {
		return nil, nil, errors.Wrap(err, `failed to marshal headers`)
	}

	buf := pool.GetBytesBuffer()
	defer pool.ReleaseBytesBuffer(buf)

	buf.WriteString(base64.EncodeToString(hdrbuf))
	buf.WriteByte('.')
	if getB64Value(hdrs) {
		buf.WriteString(base64.EncodeToString(payload))
	} else {
		buf.Write(payload)
	}

	signature, err := signer.Sign(buf.Bytes(), key)
	if err != nil {
		return nil, nil, errors.Wrap(err, `failed to sign payload`)
	}
	s.signature = signature

	buf.WriteByte('.')
	buf.WriteString(base64.EncodeToString(signature))
	ret := make([]byte, buf.Len())
	copy(ret, buf.Bytes())

	return signature, ret, nil
}

func NewMessage() *Message {
	return &Message{}
}

// Payload returns the decoded payload
func (m Message) Payload() []byte {
	return m.payload
}

func (m *Message) SetPayload(v []byte) *Message {
	m.payload = v
	return m
}

func (m Message) Signatures() []*Signature {
	return m.signatures
}

func (m *Message) AppendSignature(v *Signature) *Message {
	m.signatures = append(m.signatures, v)
	return m
}

func (m *Message) ClearSignatures() *Message {
	m.signatures = nil
	return m
}

// LookupSignature looks up a particular signature entry using
// the `kid` value
func (m Message) LookupSignature(kid string) []*Signature {
	var sigs []*Signature
	for _, sig := range m.signatures {
		if hdr := sig.PublicHeaders(); hdr != nil {
			hdrKeyID := hdr.KeyID()
			if hdrKeyID == kid {
				sigs = append(sigs, sig)
				continue
			}
		}

		if hdr := sig.ProtectedHeaders(); hdr != nil {
			hdrKeyID := hdr.KeyID()
			if hdrKeyID == kid {
				sigs = append(sigs, sig)
				continue
			}
		}
	}
	return sigs
}

type messageProxy struct {
	Payload    string            `json:"payload"` // base64 URL encoded
	Signatures []*signatureProxy `json:"signatures,omitempty"`

	// These are only available when we're using flattened JSON
	// (normally I would embed *signatureProxy, but because
	// signatureProxy is not exported, we can't use that)
	Header    *json.RawMessage `json:"header,omitempty"`
	Protected *string          `json:"protected,omitempty"`
	Signature *string          `json:"signature,omitempty"`
}

type signatureProxy struct {
	Header    json.RawMessage `json:"header"`
	Protected string          `json:"protected"`
	Signature string          `json:"signature"`
}

func (m *Message) UnmarshalJSON(buf []byte) error {
	var proxy messageProxy
	if err := json.Unmarshal(buf, &proxy); err != nil {
		return errors.Wrap(err, `failed to unmarshal into temporary structure`)
	}

	if proxy.Signature != nil {
		if len(proxy.Signatures) > 0 {
			return errors.New(`invalid format ("signatures" and "signature" keys cannot both be present)`)
		}

		var sigproxy signatureProxy
		if hdr := proxy.Header; hdr != nil {
			sigproxy.Header = *hdr
		}
		if hdr := proxy.Protected; hdr != nil {
			sigproxy.Protected = *hdr
		}
		sigproxy.Signature = *proxy.Signature

		proxy.Signatures = append(proxy.Signatures, &sigproxy)
	}

	b64 := true
	for i, sigproxy := range proxy.Signatures {
		var sig Signature

		if len(sigproxy.Header) > 0 {
			sig.headers = NewHeaders()
			if err := json.Unmarshal(sigproxy.Header, sig.headers); err != nil {
				return errors.Wrapf(err, `failed to unmarshal "header" for signature #%d`, i+1)
			}
		}

		if len(sigproxy.Protected) > 0 {
			buf, err := base64.DecodeString(sigproxy.Protected)
			if err != nil {
				return errors.Wrapf(err, `failed to decode "protected" for signature #%d`, i+1)
			}
			sig.protected = NewHeaders()
			if err := json.Unmarshal(buf, sig.protected); err != nil {
				return errors.Wrapf(err, `failed to unmarshal "protected" for signature #%d`, i+1)
			}

			if i == 0 {
				b64 = getB64Value(sig.protected)
			} else {
				if b64 != getB64Value(sig.protected) {
					return errors.Errorf(`b64 value must be the same for all signatures`)
				}
			}
		}

		if len(sigproxy.Signature) == 0 {
			return errors.Errorf(`"signature" must be non-empty for signature #%d`, i+1)
		}

		buf, err := base64.DecodeString(sigproxy.Signature)
		if err != nil {
			return errors.Wrapf(err, `failed to decode "signature" for signature #%d`, i+1)
		}
		sig.signature = buf
		m.signatures = append(m.signatures, &sig)
	}

	if !b64 {
		m.payload = []byte(proxy.Payload)
	} else {
		// Everything in the proxy is base64 encoded, except for signatures.header
		if len(proxy.Payload) == 0 {
			return errors.New(`"payload" must be non-empty`)
		}

		buf, err := base64.DecodeString(proxy.Payload)
		if err != nil {
			return errors.Wrap(err, `failed to decode payload`)
		}
		m.payload = buf
	}
	m.b64 = b64

	return nil
}

func (m Message) MarshalJSON() ([]byte, error) {
	if len(m.signatures) == 1 {
		return m.marshalFlattened()
	}
	return m.marshalFull()
}

func (m Message) marshalFlattened() ([]byte, error) {
	buf := pool.GetBytesBuffer()
	defer pool.ReleaseBytesBuffer(buf)

	sig := m.signatures[0]

	buf.WriteRune('{')
	var wrote bool

	if hdr := sig.headers; hdr != nil {
		hdrjs, err := hdr.MarshalJSON()
		if err != nil {
			return nil, errors.Wrap(err, `failed to marshal "header" (flattened format)`)
		}
		buf.WriteString(`"header":`)
		buf.Write(hdrjs)
		wrote = true
	}

	if wrote {
		buf.WriteRune(',')
	}
	buf.WriteString(`"payload":"`)
	buf.WriteString(base64.EncodeToString(m.payload))
	buf.WriteRune('"')

	if protected := sig.protected; protected != nil {
		protectedbuf, err := protected.MarshalJSON()
		if err != nil {
			return nil, errors.Wrap(err, `failed to marshal "protected" (flattened format)`)
		}
		buf.WriteString(`,"protected":"`)
		buf.WriteString(base64.EncodeToString(protectedbuf))
		buf.WriteRune('"')
	}

	buf.WriteString(`,"signature":"`)
	buf.WriteString(base64.EncodeToString(sig.signature))
	buf.WriteRune('"')
	buf.WriteRune('}')

	ret := make([]byte, buf.Len())
	copy(ret, buf.Bytes())
	return ret, nil
}

func (m Message) marshalFull() ([]byte, error) {
	buf := pool.GetBytesBuffer()
	defer pool.ReleaseBytesBuffer(buf)

	buf.WriteString(`{"payload":"`)
	buf.WriteString(base64.EncodeToString(m.payload))
	buf.WriteString(`","signatures":[`)
	for i, sig := range m.signatures {
		if i > 0 {
			buf.WriteRune(',')
		}

		buf.WriteRune('{')
		var wrote bool
		if hdr := sig.headers; hdr != nil {
			hdrbuf, err := hdr.MarshalJSON()
			if err != nil {
				return nil, errors.Wrapf(err, `failed to marshal "header" for signature #%d`, i+1)
			}
			buf.WriteString(`"header":`)
			buf.Write(hdrbuf)
			wrote = true
		}

		if protected := sig.protected; protected != nil {
			protectedbuf, err := protected.MarshalJSON()
			if err != nil {
				return nil, errors.Wrapf(err, `failed to marshal "protected" for signature #%d`, i+1)
			}
			if wrote {
				buf.WriteRune(',')
			}
			buf.WriteString(`"protected":"`)
			buf.WriteString(base64.EncodeToString(protectedbuf))
			buf.WriteRune('"')
			wrote = true
		}

		if wrote {
			buf.WriteRune(',')
		}
		buf.WriteString(`"signature":"`)
		buf.WriteString(base64.EncodeToString(sig.signature))
		buf.WriteString(`"}`)
	}
	buf.WriteString(`]}`)

	ret := make([]byte, buf.Len())
	copy(ret, buf.Bytes())
	return ret, nil
}
