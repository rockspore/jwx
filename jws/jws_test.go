package jws

import (
	"bytes"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"strings"
	"testing"

	"github.com/lestrrat/go-jwx/buffer"
	"github.com/lestrrat/go-jwx/jwa"
	"github.com/stretchr/testify/assert"
)

func TestParse_EmptyByteBuffer(t *testing.T) {
	_, err := Parse([]byte{})
	if !assert.Error(t, err, "Parsing an empty buffer should result in an error") {
		return
	}
}

const exampleCompactSerialization = `eyJ0eXAiOiJKV1QiLA0KICJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJqb2UiLA0KICJleHAiOjEzMDA4MTkzODAsDQogImh0dHA6Ly9leGFtcGxlLmNvbS9pc19yb290Ijp0cnVlfQ.dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk`

func TestParse_CompactSerializationMissingParts(t *testing.T) {
	incoming := strings.Join(
		(strings.Split(
			exampleCompactSerialization,
			".",
		))[:2],
		".",
	)
	_, err := ParseString(incoming)
	if !assert.Equal(t, ErrInvalidCompactPartsCount, err, "Parsing compact serialization with less than 3 parts should be an error") {
		return
	}
}

func TestParse_CompactSerializationBadHeader(t *testing.T) {
	parts := strings.Split(exampleCompactSerialization, ".")
	parts[0] = "%badvalue%"
	incoming := strings.Join(parts, ".")

	_, err := ParseString(incoming)
	if !assert.Error(t, err, "Parsing compact serialization with bad header should be an error") {
		return
	}
}

func TestParse_CompactSerializationBadPayload(t *testing.T) {
	parts := strings.Split(exampleCompactSerialization, ".")
	parts[1] = "%badvalue%"
	incoming := strings.Join(parts, ".")

	_, err := ParseString(incoming)
	if !assert.Error(t, err, "Parsing compact serialization with bad payload should be an error") {
		return
	}
}

func TestParse_CompactSerializationBadSignature(t *testing.T) {
	parts := strings.Split(exampleCompactSerialization, ".")
	parts[2] = "%badvalue%"
	incoming := strings.Join(parts, ".")

	t.Logf("incoming = '%s'", incoming)
	_, err := ParseString(incoming)
	if !assert.Error(t, err, "Parsing compact serialization with bad signature should be an error") {
		return
	}
}

func TestRoundtrip_Compact(t *testing.T) {
	for _, alg := range []jwa.SignatureAlgorithm{jwa.RS256, jwa.RS384, jwa.RS512, jwa.PS256, jwa.PS384, jwa.PS512} {
		key, err := rsa.GenerateKey(rand.Reader, 2048)
		if !assert.NoError(t, err, "RSA key generated") {
			return
		}

		signer, err := NewRsaSign(alg, key)
		if !assert.NoError(t, err, "RsaSign created") {
			return
		}
		hdr := NewHeader()
		hdr.Algorithm = alg
		hdr.KeyID = "foo"

		payload := buffer.Buffer("Hello, World!")
		buf, err := Encode(hdr, payload, signer)
		if !assert.NoError(t, err, "(%s) Encode is successful", alg) {
			return
		}

		for i := 0; i < 2; i++ {
			var c *Message
			switch i {
			case 0:
				c, err = Parse(buf)
				if !assert.NoError(t, err, "Parse([]byte) is successful") {
					return
				}
			case 1:
				c, err = ParseString(string(buf))
				if !assert.NoError(t, err, "ParseString(string) is successful") {
					return
				}
			default:
				panic("what?!")
			}

			if !assert.Equal(t, buffer.Buffer("Hello, World!"), c.Payload, "Payload is decoded") {
				return
			}

			if !assert.NoError(t, c.Verify(signer), "Verify is successful") {
				return
			}
		}
	}
}

const examplePayload = `{"iss":"joe",` + "\r\n" + ` "exp":1300819380,` + "\r\n" + ` "http://example.com/is_root":true}`

// TestEncode_HS256Compact tests that https://tools.ietf.org/html/rfc7515#appendix-A.1 works
func TestEncode_HS256Compact(t *testing.T) {
	const hdr = `{"typ":"JWT",` + "\r\n" + ` "alg":"HS256"}`
	const hmacKey = `AyM1SysPpbyDfgZld3umj1qzKObwVMkoqQ-EstJQLr_T-1qS0gZH75aKtMN3Yj0iPS4hcgUuTwjAzZr1Z9CAow`
	const expected = `eyJ0eXAiOiJKV1QiLA0KICJhbGciOiJIUzI1NiJ9.eyJpc3MiOiJqb2UiLA0KICJleHAiOjEzMDA4MTkzODAsDQogImh0dHA6Ly9leGFtcGxlLmNvbS9pc19yb290Ijp0cnVlfQ.dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk`

	hmacKeyDecoded := buffer.Buffer{}
	hmacKeyDecoded.Base64Decode([]byte(hmacKey))

	sign, err := NewHmacSign(jwa.HS256, hmacKeyDecoded.Bytes())
	if !assert.NoError(t, err, "HmacSign created successfully") {
		return
	}

	out, err := Encode(
		buffer.Buffer(hdr),
		buffer.Buffer(examplePayload),
		sign,
	)
	if !assert.NoError(t, err, "Encode should succeed") {
		return
	}

	if !assert.Equal(t, expected, string(out), "generated compact serialization should match") {
		return
	}

	msg, err := Parse(out)
	if !assert.NoError(t, err, "Parsing compact encoded serialization succeeds") {
		return
	}

	hdrs := msg.Signatures[0].MergedHeaders()
	if !assert.Equal(t, hdrs.Algorithm(), jwa.HS256, "Algorithm in header matches") {
		return
	}

	if !assert.NoError(t, Verify(buffer.Buffer(hdr), buffer.Buffer(examplePayload), msg.Signatures[0].Signature.Bytes(), sign), "Verify succeeds") {
		return
	}
}

func TestParse_CompactEncoded(t *testing.T) {
	// Appendix-A.4.1
	s := `eyJhbGciOiJFUzUxMiJ9.UGF5bG9hZA.AdwMgeerwtHoh-l192l60hp9wAHZFVJbLfD_UxMi70cwnZOYaRI1bKPWROc-mZZqwqT2SI-KGDKB34XO0aw_7XdtAG8GaSwFKdCAPZgoXD2YBJZCPEX3xKpRwcdOO8KpEHwJjyqOgzDO7iKvU8vcnwNrmxYbSW9ERBXukOXolLzeO_Jn`

	m, err := Parse([]byte(s))
	if !assert.NoError(t, err, "Parsing compact serialization") {
		return
	}

	// TODO: verify m
	jsonbuf, _ := json.MarshalIndent(m, "", "  ")
	t.Logf("%s", jsonbuf)
}

func TestParse_UnsecuredCompact(t *testing.T) {
	s := `eyJhbGciOiJub25lIn0.eyJpc3MiOiJqb2UiLA0KICJleHAiOjEzMDA4MTkzODAsDQogImh0dHA6Ly9leGFtcGxlLmNvbS9pc19yb290Ijp0cnVlfQ.`

	m, err := Parse([]byte(s))
	if !assert.NoError(t, err, "Parsing compact serialization") {
		return
	}

	{
		v := map[string]interface{}{}
		if !assert.NoError(t, json.Unmarshal(m.Payload.Bytes(), &v), "Unmarshal payload") {
			return
		}
		if !assert.Equal(t, v["iss"], "joe", "iss matches") {
			return
		}
		if !assert.Equal(t, int(v["exp"].(float64)), 1300819380, "exp matches") {
			return
		}
		if !assert.Equal(t, v["http://example.com/is_root"], true, "'http://example.com/is_root' matches") {
			return
		}
	}

	if !assert.Len(t, m.Signatures, 1, "There should be 1 signature") {
		return
	}

	sig := m.Signatures[0]
	if !assert.Equal(t, sig.MergedHeaders().Algorithm(), jwa.NoSignature, "Algorithm = 'none'") {
		return
	}
	if !assert.Empty(t, sig.Signature, "Signature should be empty") {
		return
	}
}

func TestParse_CompleteJSON(t *testing.T) {
	s := `{
    "payload": "eyJpc3MiOiJqb2UiLA0KICJleHAiOjEzMDA4MTkzODAsDQogImh0dHA6Ly9leGFtcGxlLmNvbS9pc19yb290Ijp0cnVlfQ",
    "signatures":[
      {
        "header": {"kid":"2010-12-29"},
        "protected":"eyJhbGciOiJSUzI1NiJ9",
        "signature": "cC4hiUPoj9Eetdgtv3hF80EGrhuB__dzERat0XF9g2VtQgr9PJbu3XOiZj5RZmh7AAuHIm4Bh-0Qc_lF5YKt_O8W2Fp5jujGbds9uJdbF9CUAr7t1dnZcAcQjbKBYNX4BAynRFdiuB--f_nZLgrnbyTyWzO75vRK5h6xBArLIARNPvkSjtQBMHlb1L07Qe7K0GarZRmB_eSN9383LcOLn6_dO--xi12jzDwusC-eOkHWEsqtFZESc6BfI7noOPqvhJ1phCnvWh6IeYI2w9QOYEUipUTI8np6LbgGY9Fs98rqVt5AXLIhWkWywlVmtVrBp0igcN_IoypGlUPQGe77Rw"
      },
      {
        "header": {"kid":"e9bc097a-ce51-4036-9562-d2ade882db0d"},
        "protected":"eyJhbGciOiJFUzI1NiJ9",
        "signature": "DtEhU3ljbEg8L38VWAfUAqOyKAM6-Xx-F4GawxaepmXFCgfTjDxw5djxLa8ISlSApmWQxfKTUJqPP3-Kg6NU1Q"
      }
    ]
  }`

	m, err := Parse([]byte(s))
	if !assert.NoError(t, err, "Parsing complete json serialization") {
		return
	}

	if !assert.Len(t, m.Signatures, 2, "There should be 2 signatures") {
		return
	}

	var sigs []Signature
	sigs = m.LookupSignature("2010-12-29")
	if !assert.Len(t, sigs, 1, "There should be 1 signature with kid = '2010-12-29'") {
		return
	}

	jsonbuf, err := json.Marshal(m)
	if !assert.NoError(t, err, "Marshal JSON is successful") {
		return
	}

	b := &bytes.Buffer{}
	json.Compact(b, jsonbuf)

	if !assert.Equal(t, b.Bytes(), jsonbuf, "generated json matches") {
		return
	}
}

func TestParse_FlattenedJSON(t *testing.T) {
	s := `{
    "payload": "eyJpc3MiOiJqb2UiLA0KICJleHAiOjEzMDA4MTkzODAsDQogImh0dHA6Ly9leGFtcGxlLmNvbS9pc19yb290Ijp0cnVlfQ",
    "protected":"eyJhbGciOiJFUzI1NiJ9",
    "header": {
      "kid":"e9bc097a-ce51-4036-9562-d2ade882db0d"
    },
    "signature": "DtEhU3ljbEg8L38VWAfUAqOyKAM6-Xx-F4GawxaepmXFCgfTjDxw5djxLa8ISlSApmWQxfKTUJqPP3-Kg6NU1Q"
  }`

	m, err := Parse([]byte(s))
	if !assert.NoError(t, err, "Parsing flattened json serialization") {
		return
	}

	if !assert.Len(t, m.Signatures, 1, "There should be 1 signature") {
		return
	}

	jsonbuf, _ := json.MarshalIndent(m, "", "  ")
	t.Logf("%s", jsonbuf)
}
