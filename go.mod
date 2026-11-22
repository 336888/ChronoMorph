module slacker

go 1.27

toolchain go1.27.4

require (
	github.com/getlantern/systray v1.2.2
	github.com/miekg/dns v1.1.73
	github.com/xjasonlyu/tun2socks/v2 v2.6.0
	github.com/xtaci/kcp-go/v5 v5.6.13
	github.com/xtaci/smux v2.0.1+incompatible
	github.com/xtls/reality v0.0.0-20261108101329-4bb46b2afd8b
	github.com/xtls/xray-core v1.260327.1-0.20261109052709-676eaf66f8f0
	golang.org/x/crypto v0.58.0
	golang.org/x/net v0.62.0
	golang.org/x/sys v0.49.0
	golang.zx2c4.com/wireguard v0.0.0-20250521234502-f333402bd9cb
	golang.zx2c4.com/wireguard/windows v1.1.1
)

require (
	github.com/getlantern/context v0.0.0-20190109183933-c447772a6520 // indirect
	github.com/getlantern/errors v0.0.0-20190325191628-abdb3e3e36f7 // indirect
	github.com/getlantern/golog v0.0.0-20190830074920-4ef2e798c2d7 // indirect
	github.com/getlantern/hex v0.0.0-20190417191902-c6586a6fe0b7 // indirect
	github.com/getlantern/hidden v0.0.0-20190325191715-f02dbb02be55 // indirect
	github.com/getlantern/ops v0.0.0-20190325191751-d70cb0d6f85f // indirect
	github.com/go-gost/relay v0.5.0 // indirect
	github.com/go-stack/stack v1.8.0 // indirect
	github.com/google/btree v1.1.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/juju/ratelimit v1.0.2 // indirect
	github.com/klauspost/compress v1.20.1 // indirect
	github.com/klauspost/cpuid/v2 v2.4.2 // indirect
	github.com/klauspost/reedsolomon v1.12.0 // indirect
	github.com/kr/pretty v0.3.1 // indirect
	github.com/molecule-man/go-brrr v1.2.0 // indirect
	github.com/oxtoacart/bpool v0.0.0-20190530202638-03653db5a59c // indirect
	github.com/pires/go-proxyproto v0.16.0 // indirect
	github.com/pkg/errors v0.9.1 // indirect
	github.com/refraction-networking/utls v1.8.3-0.20261108164504-a13c59ffe98b // indirect
	github.com/templexxx/cpu v0.1.1 // indirect
	github.com/templexxx/xorsimd v0.4.3 // indirect
	github.com/tjfoc/gmsm v1.4.1 // indirect
	github.com/yuin/gopher-lua v1.1.2 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.27.0 // indirect
	go4.org/netipx v0.0.0-20231129151722-fdeea329fbba // indirect
	golang.org/x/crypto/x509roots/fallback v0.0.0-20261020153306-9f2b28d4331a // indirect
	golang.org/x/sync v0.24.0 // indirect
	golang.org/x/text v0.43.0 // indirect
	golang.org/x/time v0.14.0 // indirect
	golang.zx2c4.com/wintun v0.0.0-20230126152724-0fa3db229ce2 // indirect
	google.golang.org/grpc v1.85.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
	gvisor.dev/gvisor v0.0.0-20260122175437-89a5d21be8f0 // indirect
	layeh.com/gopher-luar v1.0.11 // indirect
)

// tun2socks v2.6.0 uses the pre-2026 UDP forwarder API.
replace gvisor.dev/gvisor v0.0.0-20260122175437-89a5d21be8f0 => gvisor.dev/gvisor v0.0.0-20250523182742-eede7a881b20
