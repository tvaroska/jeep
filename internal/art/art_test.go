package art

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/tvaroska/jeep/internal/gemini"
)

// tinyPNG returns a valid 1x1 PNG so the fake image client returns real bytes.
func tinyPNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.RGBA{R: 10, G: 20, B: 30, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

// fakeImageClient records calls and returns a canned image result.
type fakeImageClient struct {
	data   []byte
	mime   string
	err    error
	calls  int
	prompt string // last prompt seen
}

func (f *fakeImageClient) GenerateImage(ctx context.Context, model, prompt string, inputs []gemini.FileInput, retries int) (*gemini.ImageResult, error) {
	f.calls++
	f.prompt = prompt
	if f.err != nil {
		return nil, f.err
	}
	return &gemini.ImageResult{Data: f.data, MIMEType: f.mime}, nil
}

func TestProvider_Generate(t *testing.T) {
	png := tinyPNG(t)
	fake := &fakeImageClient{data: png, mime: "image/png"}
	p := &Provider{Client: fake, Model: "stub", Mode: ModeGenerate, BookTitle: "The Book"}

	img, ok, err := p.ForChapter(context.Background(), "Chapter One", "a calm opening", nil)
	if err != nil {
		t.Fatalf("ForChapter: %v", err)
	}
	if !ok {
		t.Fatal("generate: ok = false, want true")
	}
	if len(img.Data) == 0 {
		t.Fatal("generate: empty image bytes")
	}
	if fake.calls != 1 {
		t.Errorf("generate: client calls = %d, want 1", fake.calls)
	}
}

func TestProvider_ExtractLargest(t *testing.T) {
	small := Image{Data: []byte("ab"), MIMEType: "image/jpeg"}
	big := Image{Data: []byte("abcdefgh"), MIMEType: "image/png"}
	fake := &fakeImageClient{data: tinyPNG(t), mime: "image/png"}
	p := &Provider{Client: fake, Mode: ModeExtract}

	img, ok, err := p.ForChapter(context.Background(), "Ch", "", []Image{small, big, small})
	if err != nil {
		t.Fatalf("ForChapter: %v", err)
	}
	if !ok {
		t.Fatal("extract: ok = false, want true")
	}
	if !bytes.Equal(img.Data, big.Data) {
		t.Errorf("extract: got %q, want the largest figure %q", img.Data, big.Data)
	}
	if fake.calls != 0 {
		t.Errorf("extract must not call the image client, got %d calls", fake.calls)
	}
}

func TestProvider_ExtractNoFigures(t *testing.T) {
	p := &Provider{Mode: ModeExtract}
	_, ok, err := p.ForChapter(context.Background(), "Ch", "", nil)
	if err != nil {
		t.Fatalf("ForChapter: %v", err)
	}
	if ok {
		t.Error("extract with no figures: ok = true, want false")
	}

	// Figures present but all empty also yields ok=false.
	_, ok, err = p.ForChapter(context.Background(), "Ch", "", []Image{{Data: nil}})
	if err != nil {
		t.Fatalf("ForChapter: %v", err)
	}
	if ok {
		t.Error("extract with empty figures: ok = true, want false")
	}
}

func TestProvider_None(t *testing.T) {
	p := &Provider{Mode: ModeNone}
	_, ok, err := p.ForChapter(context.Background(), "Ch", "sum", []Image{{Data: []byte("x")}})
	if err != nil {
		t.Fatalf("ForChapter: %v", err)
	}
	if ok {
		t.Error("none: ok = true, want false")
	}
}

func TestProvider_ParseMode(t *testing.T) {
	for _, in := range []string{"generate", "extract", "book-cover", "none", "GENERATE", " none "} {
		if _, err := ParseMode(in); err != nil {
			t.Errorf("ParseMode(%q) returned error: %v", in, err)
		}
	}
	if _, err := ParseMode("bogus"); err == nil {
		t.Error("ParseMode(bogus): want error, got nil")
	}
}

func TestProvider_BookCoverOnce(t *testing.T) {
	fake := &fakeImageClient{data: tinyPNG(t), mime: "image/png"}
	p := &Provider{Client: fake, Model: "stub", Mode: ModeBookCover, BookTitle: "The Book"}

	img1, ok1, err1 := p.ForChapter(context.Background(), "Chapter One", "s1", nil)
	img2, ok2, err2 := p.ForChapter(context.Background(), "Chapter Two", "s2", nil)
	if err1 != nil || err2 != nil {
		t.Fatalf("ForChapter errors: %v %v", err1, err2)
	}
	if !ok1 || !ok2 {
		t.Fatalf("book-cover: ok = %v,%v want true,true", ok1, ok2)
	}
	if fake.calls != 1 {
		t.Errorf("book-cover: client calls = %d across two chapters, want 1", fake.calls)
	}
	if !bytes.Equal(img1.Data, img2.Data) {
		t.Error("book-cover: chapters got different images, want the same memoized cover")
	}
}
