import Foundation
import Libbox
import Network
import NetworkExtension

/// Bridges the core to iOS networking. Adapted from sing-box-for-apple (GPL-3.0).
final class PlatformInterface: NSObject, LibboxPlatformInterfaceProtocol, LibboxCommandServerHandlerProtocol {
    private unowned let tunnel: PacketTunnelProvider
    private var networkSettings: NEPacketTunnelNetworkSettings?
    private var pathMonitor: NWPathMonitor?
    private var lastPath: String?

    init(_ tunnel: PacketTunnelProvider) {
        self.tunnel = tunnel
    }

    func reset() {
        networkSettings = nil
    }

    // MARK: - TUN

    func openTun(_ options: LibboxTunOptionsProtocol?, ret0_: UnsafeMutablePointer<Int32>?) throws {
        guard let options, let ret0_ else { throw TunnelError("openTun: missing arguments") }
        let settings = NEPacketTunnelNetworkSettings(tunnelRemoteAddress: "127.0.0.1")
        if options.getAutoRoute() {
            settings.mtu = NSNumber(value: options.getMTU())
            if options.getDNSMode()?.value != LibboxDNSModeDisabled {
                var servers: [String] = []
                let it = try options.getDNSServerAddress()
                while it.hasNext() { servers.append(it.next()) }
                if !servers.isEmpty { settings.dnsSettings = NEDNSSettings(servers: servers) }
            }

            var v4Addr: [String] = [], v4Mask: [String] = []
            let a4 = options.getInet4Address()!
            while a4.hasNext() { let p = a4.next()!; v4Addr.append(p.address()); v4Mask.append(p.mask()) }
            let v4 = NEIPv4Settings(addresses: v4Addr, subnetMasks: v4Mask)
            var v4Routes: [NEIPv4Route] = []
            let r4 = options.getInet4RouteAddress()!
            while r4.hasNext() { let p = r4.next()!; v4Routes.append(NEIPv4Route(destinationAddress: p.address(), subnetMask: p.mask())) }
            if v4Routes.isEmpty { v4Routes.append(NEIPv4Route.default()) }
            var v4Exclude: [NEIPv4Route] = []
            let x4 = options.getInet4RouteExcludeAddress()!
            while x4.hasNext() { let p = x4.next()!; v4Exclude.append(NEIPv4Route(destinationAddress: p.address(), subnetMask: p.mask())) }
            v4.includedRoutes = v4Routes
            v4.excludedRoutes = v4Exclude
            settings.ipv4Settings = v4

            var v6Addr: [String] = [], v6Len: [NSNumber] = []
            let a6 = options.getInet6Address()!
            while a6.hasNext() { let p = a6.next()!; v6Addr.append(p.address()); v6Len.append(NSNumber(value: p.prefix())) }
            if !v6Addr.isEmpty {
                let v6 = NEIPv6Settings(addresses: v6Addr, networkPrefixLengths: v6Len)
                var v6Routes: [NEIPv6Route] = []
                let r6 = options.getInet6RouteAddress()!
                while r6.hasNext() { let p = r6.next()!; v6Routes.append(NEIPv6Route(destinationAddress: p.address(), networkPrefixLength: NSNumber(value: p.prefix()))) }
                if v6Routes.isEmpty { v6Routes.append(NEIPv6Route.default()) }
                var v6Exclude: [NEIPv6Route] = []
                let x6 = options.getInet6RouteExcludeAddress()!
                while x6.hasNext() { let p = x6.next()!; v6Exclude.append(NEIPv6Route(destinationAddress: p.address(), networkPrefixLength: NSNumber(value: p.prefix()))) }
                v6.includedRoutes = v6Routes
                v6.excludedRoutes = v6Exclude
                settings.ipv6Settings = v6
            }
        }
        networkSettings = settings
        try blocking { try await self.tunnel.setTunnelNetworkSettings(settings) }

        if let fd = tunnel.packetFlow.value(forKeyPath: "socket.fileDescriptor") as? Int32 {
            ret0_.pointee = fd
            return
        }
        let fd = LibboxGetTunnelFileDescriptor()
        guard fd != -1 else { throw TunnelError("missing tunnel file descriptor") }
        ret0_.pointee = fd
    }

    // MARK: - Network monitoring

    func usePlatformAutoDetectControl() -> Bool { false }
    func autoDetectControl(_: Int32) throws {}

    func startDefaultInterfaceMonitor(_ listener: LibboxInterfaceUpdateListenerProtocol?) throws {
        guard let listener else { return }
        let monitor = NWPathMonitor()
        pathMonitor = monitor
        let ready = DispatchSemaphore(value: 0)
        var first = true
        monitor.pathUpdateHandler = { [weak self] path in
            self?.report(path, to: listener)
            if first { first = false; ready.signal() }
        }
        monitor.start(queue: DispatchQueue.global())
        ready.wait()
    }

    private func report(_ path: Network.NWPath, to listener: LibboxInterfaceUpdateListenerProtocol) {
        let desc = "\(path.status) " + path.availableInterfaces.map { "\($0.name)#\($0.index)" }.joined(separator: ",")
        listener.updateNetworkPath(desc)
        if desc == lastPath { return }
        lastPath = desc
        guard path.status != .unsatisfied, let iface = path.availableInterfaces.first else {
            listener.updateDefaultInterface("", interfaceIndex: -1, isExpensive: false, isConstrained: false)
            return
        }
        listener.updateDefaultInterface(iface.name, interfaceIndex: Int32(iface.index),
                                        isExpensive: path.isExpensive, isConstrained: path.isConstrained)
    }

    func closeDefaultInterfaceMonitor(_: LibboxInterfaceUpdateListenerProtocol?) throws {
        pathMonitor?.cancel()
        pathMonitor = nil
        lastPath = nil
    }

    func getInterfaces() throws -> LibboxNetworkInterfaceIteratorProtocol {
        guard let monitor = pathMonitor else { throw TunnelError("path monitor not started") }
        let path = monitor.currentPath
        if path.status == .unsatisfied { return InterfaceIterator([]) }
        let list: [LibboxNetworkInterface] = path.availableInterfaces.map { it in
            let i = LibboxNetworkInterface()
            i.name = it.name
            i.index = Int32(it.index)
            switch it.type {
            case .wifi: i.type = LibboxInterfaceTypeWIFI
            case .cellular: i.type = LibboxInterfaceTypeCellular
            case .wiredEthernet: i.type = LibboxInterfaceTypeEthernet
            default: i.type = LibboxInterfaceTypeOther
            }
            return i
        }
        return InterfaceIterator(list)
    }

    private final class InterfaceIterator: NSObject, LibboxNetworkInterfaceIteratorProtocol {
        private var items: [LibboxNetworkInterface]
        private var index = 0
        init(_ items: [LibboxNetworkInterface]) { self.items = items }
        func hasNext() -> Bool { index < items.count }
        func next() -> LibboxNetworkInterface? {
            guard index < items.count else { return nil }
            defer { index += 1 }
            return items[index]
        }
    }

    func underNetworkExtension() -> Bool { true }
    func includeAllNetworks() -> Bool { false }

    func clearDNSCache() {
        guard let settings = networkSettings else { return }
        tunnel.reasserting = true
        defer { tunnel.reasserting = false }
        try? blocking { try await self.tunnel.setTunnelNetworkSettings(nil) }
        try? blocking { try await self.tunnel.setTunnelNetworkSettings(settings) }
    }

    // MARK: - Unused platform features

    func useProcFS() -> Bool { false }
    func findConnectionOwner(_: Int32, sourceAddress _: String?, sourcePort _: Int32, destinationAddress _: String?, destinationPort _: Int32) throws -> LibboxConnectionOwner {
        throw TunnelError("not supported")
    }
    func readWIFIState() -> LibboxWIFIState? { nil }
    func localDNSTransport() -> LibboxLocalDNSTransportProtocol? { nil }
    func registerMyInterface(_: String?) {}
    func send(_: LibboxNotification?) throws {}
    func cancelNotification(_: String?, typeID _: Int32) throws {}
    func startNeighborMonitor(_: LibboxNeighborUpdateListenerProtocol?) throws {}
    func closeNeighborMonitor(_: LibboxNeighborUpdateListenerProtocol?) throws {}
    func usePlatformShell() -> Bool { false }
    func checkPlatformShell() throws { throw TunnelError("not supported") }
    func openShellSession(_: LibboxPlatformUser?, command _: String?, environ _: LibboxStringIteratorProtocol?, term _: String?, rows _: Int32, cols _: Int32) throws -> LibboxShellSessionProtocol {
        throw TunnelError("not supported")
    }
    func lookupUser(_: String?) throws -> LibboxPlatformUser { throw TunnelError("not supported") }
    func lookupSFTPServer(_ error: NSErrorPointer) -> String {
        error?.pointee = TunnelError("not supported") as NSError
        return ""
    }
    func readSystemSSHHostKey(_ error: NSErrorPointer) -> String {
        error?.pointee = TunnelError("not supported") as NSError
        return ""
    }
    func tailscaleHostname() -> String { "" }
    func usePlatformBridge() -> Bool { false }
    func createBridge(_: LibboxBridgeOptions?) throws -> LibboxBridgeSessionProtocol { throw TunnelError("not supported") }

    // MARK: - Command server handler

    func serviceStop() throws { tunnel.stopService() }
    func serviceReload() throws { try tunnel.startService() }
    func getSystemProxyStatus() throws -> LibboxSystemProxyStatus { LibboxSystemProxyStatus() }
    func setSystemProxyEnabled(_: Bool) throws {}
    func triggerNativeCrash() throws {}
    func writeDebugMessage(_ message: String?) {}
    func connectSSHAgent(_: UnsafeMutablePointer<Int32>?) throws { throw TunnelError("not supported") }

    // MARK: - Helpers

    /// The core calls us synchronously; bridge to async NetworkExtension APIs.
    private func blocking(_ body: @escaping () async throws -> Void) throws {
        let done = DispatchSemaphore(value: 0)
        var failure: Error?
        Task.detached {
            do { try await body() } catch { failure = error }
            done.signal()
        }
        done.wait()
        if let failure { throw failure }
    }
}
