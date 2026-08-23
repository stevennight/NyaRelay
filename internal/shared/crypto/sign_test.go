package crypto

import "testing"

func TestSignAndVerifyJSON(t *testing.T) {
	pub, priv, err := GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	value := map[string]any{"revision": float64(1), "node": "node_a"}
	sig, err := SignJSON(priv, value)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyJSON(pub, value, sig); err != nil {
		t.Fatal(err)
	}
	value["node"] = "node_b"
	if err := VerifyJSON(pub, value, sig); err == nil {
		t.Fatal("expected modified value to fail verification")
	}
}

func TestSignAndVerifyBytes(t *testing.T) {
	pub, priv, err := GenerateSigningKey()
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("node binary payload")
	sig, err := SignBytes(priv, payload)
	if err != nil {
		t.Fatal(err)
	}
	if err := VerifyBytes(pub, payload, sig); err != nil {
		t.Fatal(err)
	}
	if err := VerifyBytes(pub, []byte("modified payload"), sig); err == nil {
		t.Fatal("expected modified payload to fail verification")
	}
	if err := VerifyBytes(pub, payload, "bad"); err == nil {
		t.Fatal("expected invalid signature to fail verification")
	}
}
