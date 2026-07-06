package ippsrv

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"sync"
	"testing"

	"github.com/OpenPrinting/goipp"
	"github.com/rusq/thermoprint"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type captureDriver struct {
	mu  sync.Mutex
	img image.Image
}

func (d *captureDriver) SetOptions(opt ...thermoprint.Option) error { return nil }
func (d *captureDriver) PrintImage(ctx context.Context, img image.Image) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.img = img
	return nil
}
func (d *captureDriver) DPI() float64 { return 203 }
func (d *captureDriver) Width() int   { return 384 }

func (d *captureDriver) printedBounds() image.Rectangle {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.img == nil {
		return image.Rectangle{}
	}
	return d.img.Bounds()
}

func TestPrintJobMediaTrimGating(t *testing.T) {
	tests := []struct {
		name   string
		img    image.Image
		mutate func(*goipp.Message)
		wantDY int
	}{
		{
			name: "custom media keyword trims trailing blank rows",
			img: testPrintImage(t, 4, 4, map[image.Point]color.Color{
				image.Pt(0, 1): color.Black,
			}),
			mutate: func(req *goipp.Message) {
				req.Job.Add(goipp.MakeAttribute("media", goipp.TagKeyword, goipp.String("custom_48x150mm_4800x15000")))
			},
			wantDY: 2,
		},
		{
			name: "custom media-col trims trailing blank rows",
			img: testPrintImage(t, 4, 4, map[image.Point]color.Color{
				image.Pt(0, 1): color.Black,
			}),
			mutate: func(req *goipp.Message) {
				req.Job.Add(goipp.MakeAttribute("media-col", goipp.TagBeginCollection, mediaCol(rollPrintableWidth, 15000)))
			},
			wantDY: 2,
		},
		{
			name: "fixed media does not trim",
			img: testPrintImage(t, 4, 4, map[image.Point]color.Color{
				image.Pt(0, 1): color.Black,
			}),
			mutate: func(req *goipp.Message) {
				req.Job.Add(goipp.MakeAttribute("media", goipp.TagKeyword, goipp.String("om_label-48x100mm_48x100mm")))
			},
			wantDY: 4,
		},
		{
			name: "missing media does not trim",
			img: testPrintImage(t, 4, 4, map[image.Point]color.Color{
				image.Pt(0, 1): color.Black,
			}),
			wantDY: 4,
		},
		{
			name: "unparsable media does not trim",
			img: testPrintImage(t, 4, 4, map[image.Point]color.Color{
				image.Pt(0, 1): color.Black,
			}),
			mutate: func(req *goipp.Message) {
				req.Job.Add(goipp.MakeAttribute("media", goipp.TagKeyword, goipp.String("custom_roll")))
			},
			wantDY: 4,
		},
		{
			name: "all-white custom media preserves one row",
			img:  testPrintImage(t, 4, 4, nil),
			mutate: func(req *goipp.Message) {
				req.Job.Add(goipp.MakeAttribute("media", goipp.TagKeyword, goipp.String("custom_48x150mm_4800x15000")))
			},
			wantDY: 1,
		},
		{
			name: "bottom edge content is preserved",
			img: testPrintImage(t, 4, 4, map[image.Point]color.Color{
				image.Pt(0, 3): color.Black,
			}),
			mutate: func(req *goipp.Message) {
				req.Job.Add(goipp.MakeAttribute("media", goipp.TagKeyword, goipp.String("custom_48x150mm_4800x15000")))
			},
			wantDY: 4,
		},
		{
			name: "very light bottom edge content is preserved",
			img: testPrintImage(t, 4, 4, map[image.Point]color.Color{
				image.Pt(0, 3): color.Gray{Y: 250},
			}),
			mutate: func(req *goipp.Message) {
				req.Job.Add(goipp.MakeAttribute("media", goipp.TagKeyword, goipp.String("custom_48x150mm_4800x15000")))
			},
			wantDY: 4,
		},
		{
			name: "transparent trailing rows are blank",
			img: testTransparentPrintImage(t, 4, 4, map[image.Point]color.Color{
				image.Pt(0, 1): color.Black,
			}),
			mutate: func(req *goipp.Message) {
				req.Job.Add(goipp.MakeAttribute("media", goipp.TagKeyword, goipp.String("custom_48x150mm_4800x15000")))
			},
			wantDY: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			driver := &captureDriver{}
			p, err := WrapDriver(driver, "test-printer", "Test Printer")
			require.NoError(t, err)
			s, err := newBasicIPPServer("/printers/", p)
			require.NoError(t, err)
			t.Cleanup(func() {
				require.NoError(t, s.Shutdown(context.Background()))
			})

			req := newIPPRequest(goipp.OpPrintJob, testRequestID)
			if tt.mutate != nil {
				tt.mutate(req)
			}
			_, err = s.handlePrintJob(context.Background(), req, mustPNG(t, tt.img))
			require.NoError(t, err)

			assert.Equal(t, tt.wantDY, driver.printedBounds().Dy())
		})
	}
}

func TestPublicPrintDoesNotTrimTrailingBlankRows(t *testing.T) {
	driver := &captureDriver{}
	p, err := WrapDriver(driver, "test-printer", "Test Printer")
	require.NoError(t, err)

	img := testPrintImage(t, 4, 4, map[image.Point]color.Color{image.Pt(0, 1): color.Black})
	require.NoError(t, p.Print(context.Background(), mustPNG(t, img)))

	assert.Equal(t, 4, driver.printedBounds().Dy())
}

func TestPrintWithOptionsRejectsUnsupportedPrinterOptions(t *testing.T) {
	driver := &captureDriver{}
	p := optionlessPrinter{
		id:     "optionless-printer",
		driver: driver,
	}
	img := testPrintImage(t, 4, 4, map[image.Point]color.Color{image.Pt(0, 1): color.Black})

	err := printWithOptions(context.Background(), p, mustPNG(t, img), printJobOptions{trimTrailingBlank: true})

	assert.ErrorIs(t, err, ErrPrintOptionsUnsupported)
	assert.Equal(t, image.Rectangle{}, driver.printedBounds())
}

func testPrintImage(t *testing.T, width, height int, pixels map[image.Point]color.Color) image.Image {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := 0; y < height; y++ {
		for x := 0; x < width; x++ {
			img.Set(x, y, color.White)
		}
	}
	for p, c := range pixels {
		img.Set(p.X, p.Y, c)
	}
	return img
}

func testTransparentPrintImage(t *testing.T, width, height int, pixels map[image.Point]color.Color) image.Image {
	t.Helper()

	img := image.NewNRGBA(image.Rect(0, 0, width, height))
	for p, c := range pixels {
		img.Set(p.X, p.Y, c)
	}
	return img
}

func mustPNG(t *testing.T, img image.Image) []byte {
	t.Helper()

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

type optionlessPrinter struct {
	id     string
	driver Driver
}

func (p optionlessPrinter) Name() string         { return p.id }
func (p optionlessPrinter) MakeAndModel() string { return "Optionless Printer" }
func (p optionlessPrinter) Info() string         { return "Optionless Printer" }
func (p optionlessPrinter) State() PrinterState  { return PSIdle }
func (p optionlessPrinter) Ready() bool          { return true }
func (p optionlessPrinter) UpTime() int          { return 0 }
func (p optionlessPrinter) MediaSupported() []string {
	return []string{rollCustomMinMedia, rollCustomMaxMedia}
}
func (p optionlessPrinter) MediaDefault() string        { return rollCustomMinMedia }
func (p optionlessPrinter) SetState(state PrinterState) {}
func (p optionlessPrinter) UUID() string                { return p.id }
func (p optionlessPrinter) Driver() Driver              { return p.driver }
func (p optionlessPrinter) Print(ctx context.Context, data []byte) error {
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return err
	}
	return p.driver.PrintImage(ctx, img)
}
