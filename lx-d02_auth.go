package thermoprint

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
)

// The LX-D02 requires an "app check" handshake before it accepts print data:
//
//	app  -> 5a 0a + 10 random bytes
//	prn  -> 5a 0a + low bytes of crc16xmodem(rnd[i] || mac) for each i
//	app  -> 5a 0b + high bytes of crc16xmodem(rnd[i] || mac) for each i
//	prn  -> 5a 0b 01 (success) or 5a 0b 00 (failed)
//
// mac is the printer's 6-byte MAC address.  Reconstructed from the Funny Print
// app (com.lailaixiong.funnyprint 2.8.1, checkCrc/sendHandshake/sendAPPCheck).

const challengeLen = 10

func crc16xmodem(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = crc<<1 ^ 0x1021
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}

// handshakeCRC returns the bytes the printer is expected to answer with (low)
// and the bytes the app must send back (high).
func handshakeCRC(challenge, mac []byte) (low, high []byte) {
	low = make([]byte, len(challenge))
	high = make([]byte, len(challenge))
	buf := make([]byte, 1+len(mac))
	copy(buf[1:], mac)
	for i, c := range challenge {
		buf[0] = c
		crc := crc16xmodem(buf)
		high[i] = byte(crc >> 8)
		low[i] = byte(crc)
	}
	return low, high
}

// macFromInitResponse extracts the MAC from the 5a 01 response, e.g.
// 5a 01 00 03 c0 00 00 00 01 88 01 00 -> c0 00 00 00 01 88.
func macFromInitResponse(resp []byte) ([]byte, bool) {
	if len(resp) < 10 {
		return nil, false
	}
	return bytes.Clone(resp[4:10]), true
}

func macFromAddress(addr string) ([]byte, error) {
	return hex.DecodeString(strings.ReplaceAll(addr, ":", ""))
}

// appCheck performs the 5a 0a / 5a 0b handshake.
func (p *LXD02) appCheck(mac []byte) error {
	challenge := make([]byte, challengeLen)
	if _, err := rand.Read(challenge); err != nil {
		return fmt.Errorf("generate challenge: %w", err)
	}
	low, high := handshakeCRC(challenge, mac)

	resp, err := p.sendAndWait(append([]byte{0x5a, 0x0a}, challenge...), []byte{0x5a, 0x0a}, responseTimeout)
	if err != nil {
		return fmt.Errorf("handshake: %w", err)
	}
	if len(resp) < 2+challengeLen || !bytes.Equal(resp[2:2+challengeLen], low) {
		return fmt.Errorf("handshake: unexpected printer response % x (expected % x, mac % x)", resp, low, mac)
	}

	resp, err = p.sendAndWait(append([]byte{0x5a, 0x0b}, high...), []byte{0x5a, 0x0b}, responseTimeout)
	if err != nil {
		return fmt.Errorf("app check: %w", err)
	}
	if len(resp) < 3 || resp[2] != 0x01 {
		return fmt.Errorf("app check rejected by printer: % x", resp)
	}
	return nil
}
