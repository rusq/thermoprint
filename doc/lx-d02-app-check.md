# LX-D02 "app check" handshake

Some LX-D02 units refuse print data unless the host first passes an
authentication handshake. Without it the printer accepts the BLE writes,
answers `5a 0b 00` ("app check failed"), and silently discards the raster
data. No paper comes out and no `5a 06` (finished) notification arrives.

Earlier implementations sent a fixed `5a 0a` / `5a 0b` pair captured from
one specific printer. That pair only works for the unit it was captured from.

## Protocol

```
host -> 5a 0a  r0 r1 .. r9             10 random bytes
prn  -> 5a 0a  L0 L1 .. L9             Li = low byte  of crc16xmodem(ri || mac)
host -> 5a 0b  H0 H1 .. H9             Hi = high byte of crc16xmodem(ri || mac)
prn  -> 5a 0b  01 ...                  01 = success, 00 = failed
```

- `mac` is the printer's 6-byte MAC address. The printer also reports it in
  bytes 4..9 of its answer to `5a 01`, e.g.
  `5a 01 00 03 c0 00 00 00 01 88 01 00` -> `c0 00 00 00 01 88`.
- `crc16xmodem`: polynomial `0x1021`, initial value `0`, no reflection, no
  final XOR.
- The host can verify the printer's `5a 0a` answer against the expected
  low bytes before replying.

The implementation is in [`lx-d02_auth.go`](../lx-d02_auth.go). The test
vector in [`lx-d02_auth_test.go`](../lx-d02_auth_test.go) comes from a real
exchange with an LX-D02.

## How it was found

The algorithm was recovered for interoperability from the official Android
app *Funny Print* (`com.lailaixiong.funnyprint` 2.8.1). The app is built
with React Native, so its logic lives in a Hermes bytecode bundle
(`assets/index.android.bundle`, bytecode version 98).

1. [REA](https://github.com/morluto/rea) was used to triage the APK and plan
   the investigation.
2. [hermes-dec](https://github.com/P1sec/hermes-dec) decompiled the bundle.
   Function names survived, and the relevant ones are `sendHandshake`,
   `checkCrc`, `crc16xmodem`, `sendAPPCheck`, `process0x0aResp` and
   `process0x0bResp`.
3. The reconstructed algorithm was checked against a captured exchange. The
   computed low bytes matched the printer's answer byte for byte, and the
   computed high bytes were then accepted by the printer.

No code from the app is included in this repository.
