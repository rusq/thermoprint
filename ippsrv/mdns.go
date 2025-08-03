package ippsrv

import (
	"fmt"

	"github.com/grandcat/zeroconf"
)

type mdnsSvc zeroconf.Server

func newMDSN(p PrinterInformer, host string, port int) (*mdnsSvc, error) {
	const (
		serviceType = "_ipp._tcp"
		domain      = "local."
	)
	var txtRecords = [...]string{
		"txtvers=1",
		"qtotal=1",
		"rp=ipp/print",
		"ty=" + p.MakeAndModel(),
		"product=(Thermoprint)",
		"note=https://github.com/rusq/thermoprint",
		fmt.Sprintf("adminurl=http://%s:%d/", host, port),
		"priority=0",
		"kind=document,envelope",
		"pdl=application/pdf,image/urf",
		"papermax=legal-A4",
		"urf=V1.4,W8,SRGB24",
		"AirPrint=none",
	}
	srv, err := zeroconf.Register(
		p.MakeAndModel(),
		serviceType,
		domain,
		port,
		txtRecords[:],
		nil,
	)
	if err != nil {
		return nil, err
	}

	return (*mdnsSvc)(srv), nil
}

func (s *mdnsSvc) Shutdown() {
	(*zeroconf.Server)(s).Shutdown()
}
