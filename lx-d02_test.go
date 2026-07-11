package thermoprint

import "testing"

func TestParseStatus(t *testing.T) {
	st, err := parseStatus([]byte{0x5a, 0x02, 87, 1, 2, 0})
	if err != nil {
		t.Fatalf("parseStatus: %v", err)
	}
	if st.BatteryLevel != 87 || !st.NoPaper || st.Charging || !st.Charged {
		t.Fatalf("status = %+v", st)
	}
}

func TestParseStatusRejectsTruncatedPacket(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		{0x5a},
		{0x5a, 0x02},
		{0x5a, 0x02, 87, 1, 2},
	} {
		if _, err := parseStatus(data); err == nil {
			t.Fatalf("parseStatus(%x) succeeded for truncated packet", data)
		}
	}
}

func TestLXD02SnapshotStoresLastStatus(t *testing.T) {
	p := &LXD02{}
	p.connected.Store(true)
	p.options.dryrun = true
	p.state = statePrinting
	p.storeStatus(lxd02status{
		BatteryLevel: 42,
		NoPaper:      true,
		Charging:     true,
	})

	snap := p.Snapshot()
	if !snap.Connected || !snap.DryRun {
		t.Fatalf("connection snapshot = %+v", snap)
	}
	if snap.State != statePrinting.String() {
		t.Fatalf("State = %q, want %q", snap.State, statePrinting)
	}
	if snap.BatteryLevel != 42 || !snap.NoPaper || !snap.Charging || snap.Charged {
		t.Fatalf("status snapshot = %+v", snap)
	}
	if snap.LastStatusTime.IsZero() {
		t.Fatal("LastStatusTime is zero")
	}
}
