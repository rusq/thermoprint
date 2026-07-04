package ippsrv

import (
	"context"
	"image"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/rusq/thermoprint"
)

type fakeDriver struct{}

func (fakeDriver) SetOptions(opt ...thermoprint.Option) error            { return nil }
func (fakeDriver) PrintImage(ctx context.Context, img image.Image) error { return nil }
func (fakeDriver) DPI() float64                                          { return 203 }
func (fakeDriver) Width() int                                            { return 384 }

func TestTxtRecord(t *testing.T) {
	p, err := WrapDriver(fakeDriver{}, "default", "Thermal Printer")
	require.NoError(t, err)

	got := txtRecord(p, "/printers/", "myhost", 6310)

	assert.Equal(t, "printers/default", got["rp"], "rp must be derived from baseURL and printer name")
	assert.Equal(t, "1", got["txtvers"])
	assert.Equal(t, "1", got["qtotal"])
	assert.Equal(t, "Thermal Printer", got["ty"])
	assert.Equal(t, "application/pdf", got["pdl"], "pdl must only advertise formats the server can render")
	assert.Equal(t, "http://myhost.local.:6310/admin/", got["adminurl"])
	assert.Equal(t, p.UUID(), got["UUID"], "TXT UUID must be bare, without the urn:uuid: prefix")
	assert.NotContains(t, got, "urf", "must not claim AirPrint/URF support")
	assert.NotContains(t, got, "URF", "must not claim AirPrint/URF support")
}

func TestCheckAdvertisable(t *testing.T) {
	tests := []struct {
		name    string
		addr    *net.TCPAddr
		wantErr bool
	}{
		{"ipv4 loopback", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 6310}, true},
		{"ipv6 loopback", &net.TCPAddr{IP: net.IPv6loopback, Port: 6310}, true},
		{"unspecified", &net.TCPAddr{IP: net.IPv4zero, Port: 6310}, false},
		{"lan address", &net.TCPAddr{IP: net.IPv4(192, 168, 1, 10), Port: 6310}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkAdvertisable(tt.addr)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestInstanceName(t *testing.T) {
	p, err := WrapDriver(fakeDriver{}, "default", "Thermal Printer")
	require.NoError(t, err)

	assert.Equal(t, "Thermal Printer", instanceName(p, 1))
	assert.Equal(t, "Thermal Printer (default)", instanceName(p, 2))
}

func TestLocalHostname(t *testing.T) {
	got, err := localHostname()
	require.NoError(t, err)
	assert.NotEmpty(t, got)
	assert.NotContains(t, got, ".local")
}
