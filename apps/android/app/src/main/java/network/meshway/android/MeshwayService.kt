package network.meshway.android

import android.app.Notification
import android.app.NotificationChannel
import android.app.NotificationManager
import android.app.PendingIntent
import android.content.Intent
import android.net.VpnService
import android.os.ParcelFileDescriptor
import meshway.Meshway
import meshway.Node
import org.json.JSONObject
import java.io.File
import java.net.Inet4Address
import java.net.InetAddress
import java.util.concurrent.Executors
import java.util.concurrent.TimeUnit

class MeshwayService : VpnService() {
    companion object {
        const val START = "network.meshway.START"
        const val STOP = "network.meshway.STOP"
        const val CONNECT = "network.meshway.CONNECT"
        const val SERVERS = "network.meshway.SERVERS"
        const val SELECT = "network.meshway.SELECT"
        const val ACCOUNT = "network.meshway.ACCOUNT"
        const val SYNC = "network.meshway.SYNC"
        const val REVOKE = "network.meshway.REVOKE"
        const val CLEAR_HISTORY = "network.meshway.CLEAR_HISTORY"
        const val DISCONNECT = "network.meshway.DISCONNECT"
        private const val CHANNEL = "meshway-node"
        private const val NOTIFICATION = 1
    }
    private val worker = Executors.newSingleThreadScheduledExecutor()
    private var node: Node? = null
    private var tunnel: ParcelFileDescriptor? = null
    private var vpnActive = false
    private var internetPeer = ""
    private var internetAttached = false

    override fun onCreate() {
        super.onCreate()
        getSystemService(NotificationManager::class.java).createNotificationChannel(NotificationChannel(CHANNEL, "Meshway node", NotificationManager.IMPORTANCE_LOW))
        worker.scheduleWithFixedDelay({
            node?.let { active ->
                try {
                    if (vpnActive && internetPeer.isNotEmpty() && !internetAttached) attachInternet(active)
                    val raw = active.status()
                    val connected = JSONObject(raw).optJSONObject("vpn")?.optBoolean("active") ?: false
                    NodeState.update(NodeState.snapshot.copy(running = true, busy = false, vpn = vpnActive, internetVpn = internetPeer.isNotEmpty(), vpnConnected = connected, status = readableStatus(raw)))
                    if (internetPeer.isNotEmpty()) getSystemService(NotificationManager::class.java).notify(NOTIFICATION, notification(if (connected) "Internet VPN connected" else "VPN reconnecting · internet traffic blocked"))
                }
                catch (error: Exception) { NodeState.update(NodeState.snapshot.copy(message = "Status error: ${error.message}")) }
            }
        }, 3, 3, TimeUnit.SECONDS)
    }

    override fun onStartCommand(intent: Intent?, flags: Int, startId: Int): Int {
        when (intent?.action) {
            START -> {
                startForeground(NOTIFICATION, notification("Starting encrypted peer node…"))
                worker.execute { if (node == null) startNode(intent) }
            }
            STOP -> worker.execute { stopNode("Node stopped"); stopSelf() }
            CONNECT -> worker.execute {
                try { requireNotNull(node) { "Start node first" }.connect(intent.getStringExtra("address") ?: ""); refreshServers(); NodeState.update(NodeState.snapshot.copy(message = "Server added. The owner must approve this device before forwarding traffic.")) }
                catch (error: Exception) { NodeState.update(NodeState.snapshot.copy(message = "Peer connection failed: ${error.message}")) }
            }
            SERVERS -> worker.execute { try { refreshServers() } catch (error: Exception) { NodeState.update(NodeState.snapshot.copy(message = "Server list failed: ${error.message}")) } }
            SELECT -> worker.execute {
                try {
                    val active = requireNotNull(node)
                    val selected = intent.getStringExtra("peer") ?: ""
                    require(intent.getBooleanExtra("internet", false) || !vpnActive) { "Disconnect the active VPN before selecting a SOCKS connection." }
                    active.selectPeer(selected, intent.getStringExtra("address") ?: "")
                    if (intent.getBooleanExtra("internet", false)) startInternet(active, selected)
                    else NodeState.update(NodeState.snapshot.copy(message = "Server selected. SOCKS applications can use this connection."))
                    refreshServers()
                }
                catch (error: Exception) { NodeState.update(NodeState.snapshot.copy(message = "Selection failed: ${error.message}")) }
            }
            ACCOUNT -> worker.execute { try { refreshAccount() } catch (error: Exception) { NodeState.update(NodeState.snapshot.copy(message = "Account unavailable: ${error.message}")) } }
            SYNC -> worker.execute {
                try { requireNotNull(node).syncAccount(intent.getStringExtra("peer") ?: ""); refreshAccount(); NodeState.update(NodeState.snapshot.copy(message = "Account synchronized with the chosen device.")) }
                catch (error: Exception) { NodeState.update(NodeState.snapshot.copy(message = "Sync failed: ${error.message}")) }
            }
            REVOKE -> worker.execute {
                try { requireNotNull(node).revokeDevice(intent.getStringExtra("peer") ?: ""); refreshAccount(); NodeState.update(NodeState.snapshot.copy(message = "Device revoked. Trusted devices receive the signed revocation on their next sync.")) }
                catch (error: Exception) { NodeState.update(NodeState.snapshot.copy(message = "Revocation failed: ${error.message}")) }
            }
            CLEAR_HISTORY -> worker.execute {
                try { requireNotNull(node).clearHistory(intent.getBooleanExtra("synchronize", true)); refreshAccount(); NodeState.update(NodeState.snapshot.copy(message = "Connection history deleted.")) }
                catch (error: Exception) { NodeState.update(NodeState.snapshot.copy(message = "History deletion failed: ${error.message}")) }
            }
            DISCONNECT -> worker.execute {
                if (internetPeer.isNotEmpty()) {
                    node?.stopPacketVPN()
                    tunnel?.close(); tunnel = null
                    vpnActive = false; internetPeer = ""; internetAttached = false
                    node?.disconnect()
                    NodeState.update(NodeState.snapshot.copy(vpn = false, internetVpn = false, vpnConnected = false, message = "VPN disconnected. Your normal internet connection is restored."))
                }
                else if (vpnActive) { stopNode("Private VPN stopped"); stopSelf() }
                else { node?.disconnect(); NodeState.update(NodeState.snapshot.copy(message = "Disconnected from the selected exit.")) }
            }
        }
        return START_NOT_STICKY
    }

    private fun startInternet(active: Node, peer: String) {
        require(!vpnActive || internetPeer.isNotEmpty()) { "Stop your private subnet tunnel before starting an internet VPN." }
        if (internetPeer != peer) {
            active.stopPacketVPN()
            internetAttached = false
        }
        if (tunnel == null) {
            // Both address families and DNS stay in TUN. Only Meshway's own
            // transport sockets are excluded to avoid recursively tunnelling them.
            tunnel = Builder().setSession("Meshway internet VPN").setMtu(1280)
                .addAddress("10.77.0.2", 32).addAddress("fd77::2", 128)
                .addRoute("0.0.0.0", 0).addRoute("::", 0)
                .addDnsServer("10.77.0.1").addDnsServer("fd77::1")
                .addDisallowedApplication(packageName).setBlocking(true).establish()
                ?: throw IllegalStateException("Android did not establish the VPN interface")
        }
        internetPeer = peer
        vpnActive = true
        attachInternet(active)
        NodeState.update(NodeState.snapshot.copy(vpn = true, internetVpn = true, vpnConnected = internetAttached, message = if (internetAttached) "Internet VPN connected. Apps and DNS use your selected server." else "VPN reconnecting. Internet traffic stays blocked until the server responds; Disconnect restores your usual connection."))
    }

    private fun attachInternet(active: Node) {
        try { active.startInternetVPN(requireNotNull(tunnel).fd.toLong(), internetPeer); internetAttached = true }
        catch (_: Exception) { internetAttached = false }
    }

    private fun startNode(intent: Intent) {
        NodeState.update(Snapshot(busy = true, message = "Starting encrypted peer node…"))
        try {
            val config = JSONObject(File(filesDir, "config.json").readText())
            // Android owns TUN establishment; empty device prevents Linux device setup.
            config.put("vpn", JSONObject().put("enabled", intent.getBooleanExtra("vpn", false)).put("device", "").put("peer", "").put("mtu", 1280))
	            val active = Meshway.startSecure(config.toString(), StateVault.key(this))
            node = active
            if (intent.getBooleanExtra("vpn", false)) {
                val peer = intent.getStringExtra("peer") ?: ""
                require(peer.isNotEmpty()) { "VPN peer ID is required" }
                val (localAddress, localPrefix) = cidr(intent.getStringExtra("local") ?: "", privateOnly = true)
                val (routeAddress, routePrefix) = cidr(intent.getStringExtra("route") ?: "", privateOnly = true)
                val fd = Builder().setSession("Meshway private peer tunnel").setMtu(1280)
                    .addAddress(localAddress, localPrefix).addRoute(routeAddress, routePrefix)
                    // Excluding this app keeps peer transports outside the routed TUN and avoids a routing loop.
                    .addDisallowedApplication(packageName).setBlocking(true).establish()
                    ?: throw IllegalStateException("Android did not establish the VPN interface")
                tunnel = fd
                active.startVPN(fd.fd.toLong(), peer)
                vpnActive = true
            }
            NodeState.update(Snapshot(true, vpn = vpnActive, status = readableStatus(active.status()), message = "Peer node started. Connect a peer or configure bootstrap addresses."))
            refreshServers()
            refreshAccount()
            getSystemService(NotificationManager::class.java).notify(NOTIFICATION, notification(if (vpnActive) "Private peer IP tunnel active" else "Encrypted peer node running"))
        } catch (error: Exception) { stopNode("Start failed: ${error.message}"); stopSelf() }
    }

    private fun refreshServers() { node?.let { NodeState.update(NodeState.snapshot.copy(servers = it.servers())) } }
    private fun refreshAccount() {
        node?.let { active -> NodeState.update(NodeState.snapshot.copy(account = "Account\n${JSONObject(active.accountStatus()).toString(2)}\n\nDevices\n${active.devices()}\n\nLocal history\n${active.history()}")) }
    }

    private fun cidr(value: String, privateOnly: Boolean): Pair<String, Int> {
        val parts = value.split('/')
        require(parts.size == 2 && parts[0].matches(Regex("[0-9.]+"))) { "Use an IPv4 address with /prefix." }
        val address = InetAddress.getByName(parts[0])
        val prefix = parts[1].toInt()
        require(address is Inet4Address && prefix in 8..32 && !address.isAnyLocalAddress && !address.isMulticastAddress && !address.isLoopbackAddress) { "Only IPv4 private subnet routes with prefix 8–32 are supported." }
        if (privateOnly) require(address.isSiteLocalAddress) { "This initial client supports private subnet tunnels. Use a private address and route." }
        val bytes = address.address.map { it.toInt() and 255 }
        // The entire route must stay within a single RFC1918 range.
        val minimumPrefix = when { bytes[0] == 10 -> 8; bytes[0] == 172 && bytes[1] in 16..31 -> 12; bytes[0] == 192 && bytes[1] == 168 -> 16; else -> 32 }
        require(prefix >= minimumPrefix) { "Route extends outside the private address range." }
        return address.hostAddress!! to prefix
    }

    private fun readableStatus(raw: String): String {
        val status = JSONObject(raw)
        val upstream = if (status.optString("upstream_peer").isEmpty()) "No exit selected" else if (status.optBoolean("upstream_connected")) "Selected exit connection is live" else "Selected exit offline · reconnects on traffic"
        val internet = if (internetPeer.isEmpty()) "" else if (status.optJSONObject("vpn")?.optBoolean("active") == true) "Internet VPN connected · IPv4, IPv6 and DNS\n" else "Internet VPN reconnecting · traffic blocked\n"
        val listener = status.optString("socks_listen").ifEmpty { "disabled" }
        val forwards = status.optJSONObject("configuration_summary")?.optJSONArray("forwards")?.length() ?: 0
        return "$internet$upstream\nSOCKS5: $listener\nConnected peers: ${status.optJSONArray("peers")?.length() ?: 0} · configured TCP listeners: $forwards\nTraffic: ↑ ${readableBytes(status.optLong("bytes_sent"))} · ↓ ${readableBytes(status.optLong("bytes_received"))}\nSOCKS payload + VPN packets; transport overhead excluded"
    }

    private fun readableBytes(bytes: Long): String = when {
        bytes >= 1024 * 1024 -> String.format(java.util.Locale.ROOT, "%.1f MiB", bytes / (1024.0 * 1024))
        bytes >= 1024 -> String.format(java.util.Locale.ROOT, "%.1f KiB", bytes / 1024.0)
        else -> "$bytes B"
    }

    private fun stopNode(message: String) {
        NodeState.update(NodeState.snapshot.copy(busy = true, message = "Stopping…"))
        // Close the Android descriptor too: the Go core owns a duplicate of it.
        try { tunnel?.close() } catch (_: Exception) { }
        tunnel = null
        try { node?.stop() } catch (_: Exception) { }
        node = null
        vpnActive = false
        internetPeer = ""
        internetAttached = false
        stopForeground(STOP_FOREGROUND_REMOVE)
        NodeState.update(Snapshot(message = message))
    }

    private fun notification(message: String): Notification {
        val launch = PendingIntent.getActivity(this, 0, Intent(this, MainActivity::class.java), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        val stop = PendingIntent.getService(this, 1, Intent(this, MeshwayService::class.java).setAction(STOP), PendingIntent.FLAG_IMMUTABLE or PendingIntent.FLAG_UPDATE_CURRENT)
        return Notification.Builder(this, CHANNEL).setSmallIcon(android.R.drawable.stat_sys_upload).setContentTitle("Meshway")
            .setContentText(message).setContentIntent(launch).setOngoing(true)
            .addAction(Notification.Action.Builder(null, "Stop", stop).build()).build()
    }

    override fun onRevoke() { worker.execute { stopNode("Android revoked VPN permission"); stopSelf() } }
    override fun onDestroy() { worker.execute { stopNode("Node stopped"); worker.shutdown() }; super.onDestroy() }
}
