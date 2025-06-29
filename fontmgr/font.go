package fontmgr

import (
	"embed"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/rusq/fontpic"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
)

//go:embed fonts/*
var fontFS embed.FS

type BitmapFont struct {
	Name     string
	Width    uint8
	Height   uint8
	Filename string
}

var (
	errStop       = errors.New("stop")
	errDimInvalid = errors.New("dimensions invalid")
	errSkip       = errors.New("skip")
)

func LoadFontCatalogue(cb func(BitmapFont, error) error) error {
	f, err := fontFS.Open("fonts/fonts.csv")
	if err != nil {
		return fmt.Errorf("unable to find font catalogue: %w", err)
	}
	defer f.Close()
	cr := csv.NewReader(f)

	header, err := cr.Read()
	if err != nil {
		return err
	}

	for {
		row, err := cr.Read()
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		var rec = make(map[string]string)
		for i, key := range header {
			rec[key] = row[i]
		}
		fnt := BitmapFont{
			Name:     rec["name"],
			Filename: rec["file"],
		}

		width, err := atoiv[uint8](rec["dimx"], 0, 255)
		if err != nil {
			if err2 := cb(fnt, err); errors.Is(err2, errSkip) {
				continue
			} else {
				return err2
			}
		}
		fnt.Width = uint8(width)

		height, err := atoiv[uint8](rec["dimy"], 0, 255)
		if err != nil {
			if err2 := cb(fnt, err); errors.Is(err2, errSkip) {
				continue
			} else {
				return err2
			}
		}
		fnt.Height = uint8(height)

		if err := cb(fnt, nil); err != nil {
			if errors.Is(err, errStop) {
				return nil
			}
			return err
		}
	}
	return nil
}

func atoiv[T ~uint8](s string, lo, hi int) (T, error) {
	var v T
	y, err := strconv.Atoi(s)
	if err != nil {
		return v, err
	} else if y <= lo || hi < y {
		return v, fmt.Errorf("%w: %d", errDimInvalid, y)
	}
	v = T(y)
	return v, nil
}

const defaultFont = "toshiba"

var DefaultFont font.Face

func init() {
	fnt, err := LoadByName(defaultFont)
	if err != nil {
		panic(fmt.Errorf("failed to load default font %q: %w", defaultFont, err))
	}
	DefaultFont = fnt
	slog.Debug("default font loaded", "name", defaultFont)
}

func LoadFromFile(filename string, size float64, dpi float64) (font.Face, error) {
	if filename == "" {
		// load default font
		slog.Debug("filename not specified, loading the default font")
	}
	ext := filepath.Ext(strings.ToLower(filename))
	loader, ok := loadFuncs[ext]
	if !ok {
		return nil, fmt.Errorf("unsupported font type: %s", ext)
	}
	return loader(filename, size, dpi)
}

type fontLoadFunc func(filename string, size float64, dpi float64) (font.Face, error)

// loadFuncs maps file extension to appropriate font loader
var loadFuncs = map[string]fontLoadFunc{
	".bin": loadFnt,
	".fnt": loadFnt,
	".ttf": loadTTF,
	".otf": loadTTF,
}

// loadFnt loads the fnt file from disk. The height parameter is truncated to
// integer value, and the width is assumed to be 8 bits.  Font is assumed to
// contain the whole ASCII table of 256 characters.
func loadFnt(filename string, _ float64, _ float64) (font.Face, error) {
	const (
		width                = 8
		minHeight, maxHeight = 2, 32 // [minHeight, maxHeight)
	)

	fi, err := os.Stat(filename)
	if err != nil {
		return nil, err
	}
	if maxHeight*256 < fi.Size() { // 32 bytes per each char
		return nil, fmt.Errorf("unsupported file format: %s", filename)
	}
	height := fi.Size() / 256 * width

	if height <= minHeight || maxHeight < height {
		return nil, fmt.Errorf("unsupported or incorrect dimensions: %s", filename)
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	return fontpic.FntToFace(data, width, int(height)), nil

}

const maxTTFsize = 10 * 1048576 // 10 MB

// loadTTF loads a true type font and returns a face with size points.
func loadTTF(filename string, size float64, dpi float64) (font.Face, error) {
	fi, err := os.Stat(filename)
	if err != nil {
		return nil, err
	}
	if maxTTFsize < fi.Size() {
		return nil, errors.New("font file is too large")
	}
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}
	fnt, err := opentype.Parse(data)
	if err != nil {
		return nil, err
	}
	face, err := opentype.NewFace(fnt, &opentype.FaceOptions{
		Size:    size,
		DPI:     dpi,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, err
	}
	return face, nil
}

// LoadByName loads a built-in font by it's name
func LoadByName(name string) (font.Face, error) {
	var fnt *BitmapFont
	if err := LoadFontCatalogue(func(bif BitmapFont, err error) error {
		if err != nil {
			return err
		}
		if bif.Name == name {
			fnt = &bif
			return errStop
		}
		return nil

	}); err != nil {
		return nil, err
	}
	if fnt == nil {
		return nil, fmt.Errorf("font %q not found", name)
	}
	data, err := fs.ReadFile(fontFS, path.Join("fonts/", fnt.Filename))
	if err != nil {
		return nil, fmt.Errorf("error reading font file %s: %w", fnt.Filename, err)
	}

	face := fontpic.FntToFace(data, int(fnt.Width), int(fnt.Height))
	return face, nil
}
