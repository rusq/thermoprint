package printers

import (
	"image"
	"image/png"
	"os"
	"testing"
)

func TestLXD02_renderTTF(t *testing.T) {
	type args struct {
		text     string
		fontSize float64
		spacing  float64
	}
	tests := []struct {
		name    string
		prn     *LXD02
		args    args
		want    image.Image
		wantErr bool
	}{
		{
			name: "Render TTF text",
			prn: &LXD02{
				rasteriser: LXD02Rasteriser,
			},
			args: args{
				text:     "Hgllo, LXD02!\nThis is a test\nof the TrueType\nfont rendering.",
				fontSize: 8.0,
				spacing:  1.5,
			},
			want:    nil,
			wantErr: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.prn.renderTTF(tt.args.text, tt.args.fontSize, tt.args.spacing)
			if (err != nil) != tt.wantErr {
				t.Errorf("LXD02.renderTTF() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			f, err := os.Create("ttf_test_output.png")
			if err != nil {
				t.Fatalf("Failed to create output file: %v", err)
			}
			defer f.Close()
			if err := png.Encode(f, got); err != nil {
				t.Fatalf("Failed to encode image to file: %v", err)
			}
		})
	}
}
