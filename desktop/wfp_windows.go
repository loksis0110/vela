//go:build windows && (amd64 || arm64)

// Kill switch on the Windows Filtering Platform.
//
// The WFP types, constants and the set of filters (permit the tunnel
// interface, the own process, loopback, DHCP and NDP, block everything else)
// are adapted from WireGuard for Windows (tunnel/firewall), which is
// distributed under the MIT license:
//
//	Copyright (C) 2019-2026 WireGuard LLC. All Rights Reserved.
//
//	Permission is hereby granted, free of charge, to any person obtaining a
//	copy of this software and associated documentation files (the "Software"),
//	to deal in the Software without restriction, including without limitation
//	the rights to use, copy, modify, merge, publish, distribute, sublicense,
//	and/or sell copies of the Software, and to permit persons to whom the
//	Software is furnished to do so, subject to the following conditions:
//
//	The above copyright notice and this permission notice shall be included in
//	all copies or substantial portions of the Software.
//
//	THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
//	IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
//	FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
//	AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
//	LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING
//	FROM, OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER
//	DEALINGS IN THE SOFTWARE.
//
// Differences from WireGuard: the session stays open while the kill switch
// is on and the tunnel permits are swapped for a new interface LUID inside
// one transaction (no gap on reconnect); the own process is matched by its
// executable only; optional permits for local networks.

package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"runtime"
	"sync"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ---------------------------------------------------------------- WFP types

type wfpActionType uint32

const (
	fwpActionFlagTerminating wfpActionType = 0x00001000
	fwpActionBlock                         = 0x00000001 | fwpActionFlagTerminating
	fwpActionPermit                        = 0x00000002 | fwpActionFlagTerminating
)

type wfpMatchType uint32

const (
	fwpMatchEqual       wfpMatchType = 0
	fwpMatchFlagsAllSet wfpMatchType = 6
)

type wfpDataType uint32

const (
	fwpUint8           wfpDataType = 1
	fwpUint16          wfpDataType = 2
	fwpUint32          wfpDataType = 3
	fwpUint64          wfpDataType = 4
	fwpByteArray16Type wfpDataType = 11
	fwpByteBlobType    wfpDataType = 12
	fwpV4AddrMask      wfpDataType = 0x100
	fwpV6AddrMask      wfpDataType = 0x101
)

type wfpByteBlob struct {
	size uint32
	data *uint8
}

type wfpValue0 struct {
	_type wfpDataType
	value uintptr
}

type wfpDisplayData0 struct {
	name        *uint16
	description *uint16
}

type wfpFilterCondition0 struct {
	fieldKey       windows.GUID
	matchType      wfpMatchType
	conditionValue wfpValue0
}

type wfpAction0 struct {
	_type      wfpActionType
	filterType windows.GUID
}

// FWPM_FILTER0 (layout for 64-bit Windows).
type wfpFilter0 struct {
	filterKey           windows.GUID
	displayData         wfpDisplayData0
	flags               uint32
	providerKey         *windows.GUID
	providerData        wfpByteBlob
	layerKey            windows.GUID
	subLayerKey         windows.GUID
	weight              wfpValue0
	numFilterConditions uint32
	filterCondition     *wfpFilterCondition0
	action              wfpAction0
	offset1             [4]byte
	providerContextKey  windows.GUID
	reserved            *windows.GUID
	filterID            uint64
	effectiveWeight     wfpValue0
}

type wfpSession0 struct {
	sessionKey           windows.GUID
	displayData          wfpDisplayData0
	flags                uint32
	txnWaitTimeoutInMSec uint32
	processID            uint32
	sid                  *windows.SID
	username             *uint16
	kernelMode           uint8
}

type wfpSublayer0 struct {
	subLayerKey  windows.GUID
	displayData  wfpDisplayData0
	flags        uint32
	providerKey  *windows.GUID
	providerData wfpByteBlob
	weight       uint16
}

type wfpProvider0 struct {
	providerKey  windows.GUID
	displayData  wfpDisplayData0
	flags        uint32
	providerData wfpByteBlob
	serviceName  *uint16
}

type wfpV4AddrAndMask struct {
	addr uint32
	mask uint32
}

type wfpV6AddrAndMask struct {
	addr         [16]uint8
	prefixLength uint8
}

type wfpByteArray16 struct {
	byteArray16 [16]uint8
}

const (
	fwpmSessionFlagDynamic = 0x00000001
	rpcCAuthnWinNT         = 10
	fwpConditionIsLoopback = 0x00000001

	ipprotoICMPv6 = 58
	ipprotoUDP    = 17
)

var (
	fwpmConditionIPLocalInterface = windows.GUID{Data1: 0x4cd62a49, Data2: 0x59c3, Data3: 0x4969, Data4: [8]byte{0xb7, 0xf3, 0xbd, 0xa5, 0xd3, 0x28, 0x90, 0xa4}}
	fwpmConditionIPRemoteAddress  = windows.GUID{Data1: 0xb235ae9a, Data2: 0x1d64, Data3: 0x49b8, Data4: [8]byte{0xa4, 0x4c, 0x5f, 0xf3, 0xd9, 0x09, 0x50, 0x45}}
	fwpmConditionIPLocalAddress   = windows.GUID{Data1: 0xd9ee00de, Data2: 0xc1ef, Data3: 0x4617, Data4: [8]byte{0xbf, 0xe3, 0xff, 0xd8, 0xf5, 0xa0, 0x89, 0x57}}
	fwpmConditionIPProtocol       = windows.GUID{Data1: 0x3971ef2b, Data2: 0x623e, Data3: 0x4f9a, Data4: [8]byte{0x8c, 0xb1, 0x6e, 0x79, 0xb8, 0x06, 0xb9, 0xa7}}
	fwpmConditionIPLocalPort      = windows.GUID{Data1: 0x0c1ba1af, Data2: 0x5765, Data3: 0x453f, Data4: [8]byte{0xaf, 0x22, 0xa8, 0xf7, 0x91, 0xac, 0x77, 0x5b}}
	fwpmConditionIPRemotePort     = windows.GUID{Data1: 0xc35a604d, Data2: 0xd22b, Data3: 0x4e1a, Data4: [8]byte{0x91, 0xb4, 0x68, 0xf6, 0x74, 0xee, 0x67, 0x4b}}
	fwpmConditionALEAppID         = windows.GUID{Data1: 0xd78e1e87, Data2: 0x8644, Data3: 0x4ea5, Data4: [8]byte{0x94, 0x37, 0xd8, 0x09, 0xec, 0xef, 0xc9, 0x71}}
	fwpmConditionFlags            = windows.GUID{Data1: 0x632ce23b, Data2: 0x5167, Data3: 0x435c, Data4: [8]byte{0x86, 0xd7, 0xe9, 0x03, 0x68, 0x4a, 0xa8, 0x0c}}
	fwpmConditionICMPType         = fwpmConditionIPLocalPort
	fwpmConditionICMPCode         = fwpmConditionIPRemotePort

	fwpmLayerALEAuthConnectV4    = windows.GUID{Data1: 0xc38d57d1, Data2: 0x05a7, Data3: 0x4c33, Data4: [8]byte{0x90, 0x4f, 0x7f, 0xbc, 0xee, 0xe6, 0x0e, 0x82}}
	fwpmLayerALEAuthRecvAcceptV4 = windows.GUID{Data1: 0xe1cd9fe7, Data2: 0xf4b5, Data3: 0x4273, Data4: [8]byte{0x96, 0xc0, 0x59, 0x2e, 0x48, 0x7b, 0x86, 0x50}}
	fwpmLayerALEAuthConnectV6    = windows.GUID{Data1: 0x4a72393b, Data2: 0x319f, Data3: 0x44bc, Data4: [8]byte{0x84, 0xc3, 0xba, 0x54, 0xdc, 0xb3, 0xb6, 0xb4}}
	fwpmLayerALEAuthRecvAcceptV6 = windows.GUID{Data1: 0xa3b42c97, Data2: 0x9f04, Data3: 0x4672, Data4: [8]byte{0xb8, 0x7e, 0xce, 0xe9, 0xc4, 0x83, 0x25, 0x7f}}

	// IPv6 link-local and multicast addresses used by DHCPv6 and NDP.
	wfpLinkLocal                = wfpV6AddrAndMask{[16]uint8{0xfe, 0x80}, 10}
	wfpLinkLocalDHCPMulticast   = wfpByteArray16{[16]uint8{0xff, 0x02, 13: 0x01, 15: 0x02}}
	wfpSiteLocalDHCPMulticast   = wfpByteArray16{[16]uint8{0xff, 0x05, 13: 0x01, 15: 0x03}}
	wfpLinkLocalRouterMulticast = wfpByteArray16{[16]uint8{0xff, 0x02, 15: 0x02}}
)

// ---------------------------------------------------------------- syscalls

var (
	modFwpuclnt                   = windows.NewLazySystemDLL("fwpuclnt.dll")
	procFwpmEngineOpen0           = modFwpuclnt.NewProc("FwpmEngineOpen0")
	procFwpmEngineClose0          = modFwpuclnt.NewProc("FwpmEngineClose0")
	procFwpmSubLayerAdd0          = modFwpuclnt.NewProc("FwpmSubLayerAdd0")
	procFwpmProviderAdd0          = modFwpuclnt.NewProc("FwpmProviderAdd0")
	procFwpmFilterAdd0            = modFwpuclnt.NewProc("FwpmFilterAdd0")
	procFwpmFilterDeleteByID0     = modFwpuclnt.NewProc("FwpmFilterDeleteById0")
	procFwpmGetAppIDFromFileName0 = modFwpuclnt.NewProc("FwpmGetAppIdFromFileName0")
	procFwpmFreeMemory0           = modFwpuclnt.NewProc("FwpmFreeMemory0")
	procFwpmTransactionBegin0     = modFwpuclnt.NewProc("FwpmTransactionBegin0")
	procFwpmTransactionCommit0    = modFwpuclnt.NewProc("FwpmTransactionCommit0")
	procFwpmTransactionAbort0     = modFwpuclnt.NewProc("FwpmTransactionAbort0")
)

// wfpCall calls a WFP function that returns a Win32 error code. Like
// LazyProc.Call, it keeps the memory behind pointer arguments on the heap
// for the duration of the call.
//
//go:uintptrescapes
func wfpCall(p *windows.LazyProc, args ...uintptr) error {
	if err := p.Find(); err != nil {
		return err
	}
	r, _, _ := syscall.SyscallN(p.Addr(), args...)
	if r != 0 {
		return syscall.Errno(r)
	}
	return nil
}

func wfpText(s string) *uint16 {
	p, _ := windows.UTF16PtrFromString(s)
	return p
}

// ---------------------------------------------------------------- firewall

type wfpFirewall struct {
	mu         sync.Mutex
	session    uintptr
	provider   windows.GUID
	sublayer   windows.GUID
	lanFilters []uint64
	tunFilters []uint64
	tunLUID    uint64
}

func newFirewall() firewall { return &wfpFirewall{} }

func killSwitchSupported() bool { return isElevated() }

func (f *wfpFirewall) Active() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.session != 0
}

// Enable blocks all traffic except Vela itself, loopback, DHCP/NDP and,
// when allowed, local networks. Calling it again only updates the LAN
// permits.
func (f *wfpFirewall) Enable(allowLAN bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.session != 0 {
		if allowLAN == (len(f.lanFilters) > 0) {
			return nil
		}
		// the ids change only when the transaction is committed
		var ids []uint64
		err := f.transaction(func() error {
			if allowLAN {
				var err error
				ids, err = f.permitLAN(12)
				return err
			}
			for _, id := range f.lanFilters {
				if err := wfpCall(procFwpmFilterDeleteByID0, f.session, uintptr(id)); err != nil {
					return err
				}
			}
			return nil
		})
		if err == nil {
			f.lanFilters = ids
		}
		return err
	}

	session := uintptr(0)
	sess := wfpSession0{
		displayData:          wfpDisplayData0{name: wfpText("Vela"), description: wfpText("Vela kill switch")},
		flags:                fwpmSessionFlagDynamic, // filters vanish when Vela exits or crashes
		txnWaitTimeoutInMSec: windows.INFINITE,
	}
	if err := wfpCall(procFwpmEngineOpen0, 0, rpcCAuthnWinNT, 0, uintptr(unsafe.Pointer(&sess)), uintptr(unsafe.Pointer(&session))); err != nil {
		return fmt.Errorf("WFP: %w", err)
	}
	f.session = session
	err := f.transaction(func() error {
		var err error
		if f.provider, err = windows.GenerateGUID(); err != nil {
			return err
		}
		if f.sublayer, err = windows.GenerateGUID(); err != nil {
			return err
		}
		provider := wfpProvider0{providerKey: f.provider, displayData: wfpDisplayData0{name: wfpText("Vela"), description: wfpText("Vela provider")}}
		if err := wfpCall(procFwpmProviderAdd0, f.session, uintptr(unsafe.Pointer(&provider)), 0); err != nil {
			return err
		}
		sublayer := wfpSublayer0{
			subLayerKey: f.sublayer,
			displayData: wfpDisplayData0{name: wfpText("Vela filters"), description: wfpText("Kill switch: permit the VPN, block the rest")},
			providerKey: &f.provider,
			weight:      ^uint16(0),
		}
		if err := wfpCall(procFwpmSubLayerAdd0, f.session, uintptr(unsafe.Pointer(&sublayer)), 0); err != nil {
			return err
		}
		runtime.KeepAlive(provider)
		if err := f.permitOwnProcess(15); err != nil {
			return err
		}
		if err := f.permitLoopback(13); err != nil {
			return err
		}
		if err := f.permitDHCPAndNDP(12); err != nil {
			return err
		}
		if allowLAN {
			ids, err := f.permitLAN(12)
			if err != nil {
				return err
			}
			f.lanFilters = ids
		}
		return f.blockAll(0)
	})
	if err != nil {
		wfpCall(procFwpmEngineClose0, f.session)
		f.session = 0
		f.lanFilters = nil
		return err
	}
	return nil
}

// SetTunnel permits traffic through the VPN interface. The permits of the
// previous interface are removed in the same transaction, so there is no
// moment when the new tunnel is blocked or the old one stays open.
func (f *wfpFirewall) SetTunnel(luid uint64) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.session == 0 {
		return errors.New("kill switch is off")
	}
	if luid == f.tunLUID && len(f.tunFilters) > 0 {
		return nil
	}
	var ids []uint64
	err := f.transaction(func() error {
		var err error
		if ids, err = f.permitInterface(luid, 12); err != nil {
			return err
		}
		for _, id := range f.tunFilters {
			if err := wfpCall(procFwpmFilterDeleteByID0, f.session, uintptr(id)); err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		f.tunFilters, f.tunLUID = ids, luid
	}
	return err
}

// Disable removes every filter by closing the dynamic session.
func (f *wfpFirewall) Disable() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.session != 0 {
		wfpCall(procFwpmEngineClose0, f.session)
	}
	f.session, f.lanFilters, f.tunFilters, f.tunLUID = 0, nil, nil, 0
}

func (f *wfpFirewall) transaction(fn func() error) error {
	if err := wfpCall(procFwpmTransactionBegin0, f.session, 0); err != nil {
		return fmt.Errorf("WFP: %w", err)
	}
	if err := fn(); err != nil {
		wfpCall(procFwpmTransactionAbort0, f.session)
		return fmt.Errorf("WFP: %w", err)
	}
	if err := wfpCall(procFwpmTransactionCommit0, f.session); err != nil {
		wfpCall(procFwpmTransactionAbort0, f.session)
		return fmt.Errorf("WFP: %w", err)
	}
	return nil
}

// addFilter adds one filter and returns its id.
func (f *wfpFirewall) addFilter(name string, layer windows.GUID, weight uint8, action wfpActionType, conds []wfpFilterCondition0) (uint64, error) {
	filter := wfpFilter0{
		displayData: wfpDisplayData0{name: wfpText(name), description: wfpText("")},
		providerKey: &f.provider,
		layerKey:    layer,
		subLayerKey: f.sublayer,
		weight:      wfpValue0{_type: fwpUint8, value: uintptr(weight)},
		action:      wfpAction0{_type: action},
	}
	// WFP reads the conditions through pointers the Go compiler can't see:
	// pinning keeps them on the heap and in place during the call
	var pin runtime.Pinner
	defer pin.Unpin()
	if len(conds) > 0 {
		pin.Pin(&conds[0])
		filter.numFilterConditions = uint32(len(conds))
		filter.filterCondition = &conds[0]
	}
	id := uint64(0)
	err := wfpCall(procFwpmFilterAdd0, f.session, uintptr(unsafe.Pointer(&filter)), 0, uintptr(unsafe.Pointer(&id)))
	runtime.KeepAlive(conds)
	runtime.KeepAlive(filter)
	return id, err
}

var allALELayers = []struct {
	name  string
	layer windows.GUID
}{
	{"outbound IPv4", fwpmLayerALEAuthConnectV4},
	{"inbound IPv4", fwpmLayerALEAuthRecvAcceptV4},
	{"outbound IPv6", fwpmLayerALEAuthConnectV6},
	{"inbound IPv6", fwpmLayerALEAuthRecvAcceptV6},
}

func (f *wfpFirewall) permitInterface(luid uint64, weight uint8) ([]uint64, error) {
	// the condition refers to the value by address: keep it on the heap
	var pin runtime.Pinner
	defer pin.Unpin()
	value := new(uint64)
	*value = luid
	pin.Pin(value)
	conds := []wfpFilterCondition0{{
		fieldKey:       fwpmConditionIPLocalInterface,
		matchType:      fwpMatchEqual,
		conditionValue: wfpValue0{_type: fwpUint64, value: uintptr(unsafe.Pointer(value))},
	}}
	var ids []uint64
	for _, l := range allALELayers {
		id, err := f.addFilter("Permit "+l.name+" on the Vela tunnel", l.layer, weight, fwpActionPermit, conds)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	runtime.KeepAlive(value)
	return ids, nil
}

// permitOwnProcess lets Vela.exe reach the servers (it carries the VPN
// traffic) and download subscriptions and updates.
func (f *wfpFirewall) permitOwnProcess(weight uint8) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	var appID *wfpByteBlob
	if err := wfpCall(procFwpmGetAppIDFromFileName0, uintptr(unsafe.Pointer(wfpText(exe))), uintptr(unsafe.Pointer(&appID))); err != nil {
		return err
	}
	defer procFwpmFreeMemory0.Call(uintptr(unsafe.Pointer(&appID)))
	conds := []wfpFilterCondition0{{
		fieldKey:       fwpmConditionALEAppID,
		matchType:      fwpMatchEqual,
		conditionValue: wfpValue0{_type: fwpByteBlobType, value: uintptr(unsafe.Pointer(appID))},
	}}
	for _, l := range allALELayers {
		if _, err := f.addFilter("Permit "+l.name+" for Vela", l.layer, weight, fwpActionPermit, conds); err != nil {
			return err
		}
	}
	return nil
}

func (f *wfpFirewall) permitLoopback(weight uint8) error {
	conds := []wfpFilterCondition0{{
		fieldKey:       fwpmConditionFlags,
		matchType:      fwpMatchFlagsAllSet,
		conditionValue: wfpValue0{_type: fwpUint32, value: fwpConditionIsLoopback},
	}}
	for _, l := range allALELayers {
		if _, err := f.addFilter("Permit "+l.name+" on loopback", l.layer, weight, fwpActionPermit, conds); err != nil {
			return err
		}
	}
	return nil
}

func condU8(key windows.GUID, v uint8) wfpFilterCondition0 {
	return wfpFilterCondition0{fieldKey: key, matchType: fwpMatchEqual, conditionValue: wfpValue0{_type: fwpUint8, value: uintptr(v)}}
}

func condU16(key windows.GUID, v uint16) wfpFilterCondition0 {
	return wfpFilterCondition0{fieldKey: key, matchType: fwpMatchEqual, conditionValue: wfpValue0{_type: fwpUint16, value: uintptr(v)}}
}

func condPtr(key windows.GUID, t wfpDataType, p unsafe.Pointer) wfpFilterCondition0 {
	return wfpFilterCondition0{fieldKey: key, matchType: fwpMatchEqual, conditionValue: wfpValue0{_type: t, value: uintptr(p)}}
}

// permitDHCPAndNDP keeps the computer's own address configuration working
// (DHCPv4, DHCPv6 and IPv6 neighbor discovery).
func (f *wfpFirewall) permitDHCPAndNDP(weight uint8) error {
	type def struct {
		name  string
		layer windows.GUID
		conds []wfpFilterCondition0
	}
	icmp := func(t uint16) []wfpFilterCondition0 {
		return []wfpFilterCondition0{
			condU8(fwpmConditionIPProtocol, ipprotoICMPv6),
			condU16(fwpmConditionICMPType, t),
			condU16(fwpmConditionICMPCode, 0),
		}
	}
	defs := []def{
		{"Permit outbound DHCP request (IPv4)", fwpmLayerALEAuthConnectV4, []wfpFilterCondition0{
			condU8(fwpmConditionIPProtocol, ipprotoUDP),
			condU16(fwpmConditionIPLocalPort, 68),
			condU16(fwpmConditionIPRemotePort, 67),
			{fieldKey: fwpmConditionIPRemoteAddress, matchType: fwpMatchEqual, conditionValue: wfpValue0{_type: fwpUint32, value: 0xffffffff}},
		}},
		{"Permit inbound DHCP response (IPv4)", fwpmLayerALEAuthRecvAcceptV4, []wfpFilterCondition0{
			condU8(fwpmConditionIPProtocol, ipprotoUDP),
			condU16(fwpmConditionIPLocalPort, 68),
			condU16(fwpmConditionIPRemotePort, 67),
		}},
		{"Permit outbound DHCP request (IPv6)", fwpmLayerALEAuthConnectV6, []wfpFilterCondition0{
			condU8(fwpmConditionIPProtocol, ipprotoUDP),
			condPtr(fwpmConditionIPRemoteAddress, fwpByteArray16Type, unsafe.Pointer(&wfpLinkLocalDHCPMulticast)),
			condPtr(fwpmConditionIPRemoteAddress, fwpByteArray16Type, unsafe.Pointer(&wfpSiteLocalDHCPMulticast)),
			condU16(fwpmConditionIPRemotePort, 547),
			condPtr(fwpmConditionIPLocalAddress, fwpV6AddrMask, unsafe.Pointer(&wfpLinkLocal)),
			condU16(fwpmConditionIPLocalPort, 546),
		}},
		{"Permit inbound DHCP response (IPv6)", fwpmLayerALEAuthRecvAcceptV6, []wfpFilterCondition0{
			condU8(fwpmConditionIPProtocol, ipprotoUDP),
			condPtr(fwpmConditionIPRemoteAddress, fwpV6AddrMask, unsafe.Pointer(&wfpLinkLocal)),
			condU16(fwpmConditionIPRemotePort, 547),
			condPtr(fwpmConditionIPLocalAddress, fwpV6AddrMask, unsafe.Pointer(&wfpLinkLocal)),
			condU16(fwpmConditionIPLocalPort, 546),
		}},
		// router solicitation, outgoing to ff02::2
		{"Permit NDP type 133", fwpmLayerALEAuthConnectV6, append(icmp(133),
			condPtr(fwpmConditionIPRemoteAddress, fwpByteArray16Type, unsafe.Pointer(&wfpLinkLocalRouterMulticast)))},
		// router advertisement, incoming from fe80::/10
		{"Permit NDP type 134", fwpmLayerALEAuthRecvAcceptV6, append(icmp(134),
			condPtr(fwpmConditionIPRemoteAddress, fwpV6AddrMask, unsafe.Pointer(&wfpLinkLocal)))},
		// neighbor solicitation and advertisement, both directions
		{"Permit NDP type 135", fwpmLayerALEAuthConnectV6, icmp(135)},
		{"Permit NDP type 135", fwpmLayerALEAuthRecvAcceptV6, icmp(135)},
		{"Permit NDP type 136", fwpmLayerALEAuthConnectV6, icmp(136)},
		{"Permit NDP type 136", fwpmLayerALEAuthRecvAcceptV6, icmp(136)},
		// redirect, incoming from fe80::/10
		{"Permit NDP type 137", fwpmLayerALEAuthRecvAcceptV6, append(icmp(137),
			condPtr(fwpmConditionIPRemoteAddress, fwpV6AddrMask, unsafe.Pointer(&wfpLinkLocal)))},
	}
	for _, d := range defs {
		if _, err := f.addFilter(d.name, d.layer, weight, fwpActionPermit, d.conds); err != nil {
			return err
		}
	}
	return nil
}

// Local networks: private ranges, link-local, multicast and broadcast.
var (
	lanV4 = []netip.Prefix{
		netip.MustParsePrefix("10.0.0.0/8"),
		netip.MustParsePrefix("172.16.0.0/12"),
		netip.MustParsePrefix("192.168.0.0/16"),
		netip.MustParsePrefix("169.254.0.0/16"),
		netip.MustParsePrefix("224.0.0.0/4"),
		netip.MustParsePrefix("255.255.255.255/32"),
	}
	lanV6 = []netip.Prefix{
		netip.MustParsePrefix("fe80::/10"),
		netip.MustParsePrefix("fc00::/7"),
		netip.MustParsePrefix("ff00::/8"),
	}
)

func (f *wfpFirewall) permitLAN(weight uint8) ([]uint64, error) {
	v4 := make([]wfpV4AddrAndMask, len(lanV4))
	conds4 := make([]wfpFilterCondition0, len(lanV4))
	for i, p := range lanV4 {
		addr := p.Addr().As4()
		v4[i] = wfpV4AddrAndMask{addr: binary.BigEndian.Uint32(addr[:]), mask: ^uint32(0) << (32 - p.Bits())}
		// conditions with the same field are combined with OR
		conds4[i] = condPtr(fwpmConditionIPRemoteAddress, fwpV4AddrMask, unsafe.Pointer(&v4[i]))
	}
	v6 := make([]wfpV6AddrAndMask, len(lanV6))
	conds6 := make([]wfpFilterCondition0, len(lanV6))
	for i, p := range lanV6 {
		v6[i] = wfpV6AddrAndMask{addr: p.Addr().As16(), prefixLength: uint8(p.Bits())}
		conds6[i] = condPtr(fwpmConditionIPRemoteAddress, fwpV6AddrMask, unsafe.Pointer(&v6[i]))
	}
	var pin runtime.Pinner
	defer pin.Unpin()
	pin.Pin(&v4[0])
	pin.Pin(&v6[0])
	var ids []uint64
	for _, l := range allALELayers {
		conds := conds4
		if l.layer == fwpmLayerALEAuthConnectV6 || l.layer == fwpmLayerALEAuthRecvAcceptV6 {
			conds = conds6
		}
		id, err := f.addFilter("Permit "+l.name+" in local networks", l.layer, weight, fwpActionPermit, conds)
		if err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	runtime.KeepAlive(v4)
	runtime.KeepAlive(v6)
	return ids, nil
}

func (f *wfpFirewall) blockAll(weight uint8) error {
	for _, l := range allALELayers {
		if _, err := f.addFilter("Block all "+l.name, l.layer, weight, fwpActionBlock, nil); err != nil {
			return err
		}
	}
	return nil
}
