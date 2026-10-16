package bcwd

import (
	"bytes"
	"math/big"
	"os"
	"testing"
)

func TestRoundTrip(t *testing.T) {
	original, err := os.ReadFile("wdbc.data")
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := Compress(original)
	if err != nil {
		t.Fatalf("compress: %v", err)
	}
	got, err := Decompress(compressed)
	if err != nil {
		t.Fatalf("decompress: %v", err)
	}
	if !bytes.Equal(got, original) {
		t.Fatal("round trip mismatch")
	}
	again, err := Compress(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(again, compressed) {
		t.Fatal("encoding is not stable")
	}
	t.Logf("%d -> %d bytes", len(original), len(compressed))
}

func TestWidths(t *testing.T) {
	exp := big.NewInt(rowCount)
	sum := 0
	for j, base := range bases {
		max := new(big.Int).Exp(big.NewInt(int64(base)), exp, nil)
		max.Sub(max, big.NewInt(1))
		width := (max.BitLen() + 7) / 8
		if width != widths[j] {
			t.Fatalf("col %d width %d, bit length wants %d", j, widths[j], width)
		}
		if (width-1)*8 >= max.BitLen() {
			t.Fatalf("col %d fits in %d bytes", j, width-1)
		}
		sum += width
	}
	if sum != featureBytes {
		t.Fatalf("feature bytes %d, want %d", featureBytes, sum)
	}
}

func TestBoundsAreTight(t *testing.T) {
	original, err := os.ReadFile("wdbc.data")
	if err != nil {
		t.Fatal(err)
	}
	_, _, ranks, err := parse(original)
	if err != nil {
		t.Fatal(err)
	}
	seenMin := [featCount]bool{}
	seenMax := [featCount]bool{}
	for i := range rowCount {
		for j := range featCount {
			sig, exp, isZero, err := unrank(j, ranks[i][j])
			if err != nil {
				t.Fatal(err)
			}
			scaled := 0
			if !isZero {
				scaled = sig * pow10(exp-bounds[j][0])
			}
			if scaled == bounds[j][2] {
				seenMin[j] = true
			}
			if scaled == bounds[j][3] {
				seenMax[j] = true
			}
		}
	}
	for j := range featCount {
		if !seenMin[j] || !seenMax[j] {
			t.Errorf("feature %d range is loose", j)
		}
	}
}

func TestRejectsGarbage(t *testing.T) {
	original, err := os.ReadFile("wdbc.data")
	if err != nil {
		t.Fatal(err)
	}
	compressed, err := Compress(original)
	if err != nil {
		t.Fatal(err)
	}
	flipped := bytes.Clone(compressed)
	flipped[0] |= 0x80
	if _, err := Decompress(flipped); err != ErrFormat {
		t.Fatalf("high bit: got %v", err)
	}
	if _, err := Decompress(compressed[:10]); err != ErrFormat {
		t.Fatalf("short: got %v", err)
	}

	broken := bytes.Clone(original)
	// First feature of the first row is "17.99". Five significant digits
	// is outside the model.
	old := []byte("842302,M,17.99,")
	next := []byte("842302,M,17.991,")
	if !bytes.HasPrefix(broken, old) {
		t.Fatal("unexpected file prefix")
	}
	broken = append(next, broken[len(old):]...)
	if _, err := Compress(broken); err != ErrFormat {
		t.Fatalf("extra digit: got %v", err)
	}

	badID := bytes.Clone(original)
	badID = append([]byte{'0'}, badID...)
	if _, err := Compress(badID); err != ErrFormat {
		t.Fatalf("leading zero id: got %v", err)
	}
}
