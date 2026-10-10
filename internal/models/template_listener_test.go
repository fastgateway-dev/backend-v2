package models

import "testing"

func TestListenersValueScanRoundtrip(t *testing.T) {
	in := Listeners{
		{Name: "http", Protocol: ListenerHTTP, Port: 80},
		{Name: "https", Protocol: ListenerHTTPS, Port: 443, TLSMode: TLSListenerTerminate},
		{Name: "tcpudp", Protocol: ListenerTCP, PortRangeMin: 9000, PortRangeMax: 9100},
	}
	v, err := in.Value()
	if err != nil {
		t.Fatalf("Value: %v", err)
	}
	var out Listeners
	if err := out.Scan(v); err != nil {
		t.Fatalf("Scan: %v", err)
	}
	if len(out) != 3 || out[1].Name != "https" || out[1].TLSMode != TLSListenerTerminate {
		t.Fatalf("roundtrip mismatch: %+v", out)
	}
	// nil scan => empty, no error (nullable column)
	var n Listeners
	if err := n.Scan(nil); err != nil || n != nil {
		t.Fatalf("nil scan: got %+v err %v", n, err)
	}
}

func TestListenersHelpers(t *testing.T) {
	ls := Listeners{
		{Name: "https", Protocol: ListenerHTTPS, Port: 443},
		{Name: "tcpudp", Protocol: ListenerTCP, PortRangeMin: 9000, PortRangeMax: 9100},
	}
	if got := ls.HostnameRouted(); len(got) != 1 || got[0].Protocol != ListenerHTTPS {
		t.Fatalf("HostnameRouted: %+v", got)
	}
	min, max, ok := ls.StreamRange()
	if !ok || min != 9000 || max != 9100 {
		t.Fatalf("StreamRange: %d-%d ok=%v", min, max, ok)
	}
}
