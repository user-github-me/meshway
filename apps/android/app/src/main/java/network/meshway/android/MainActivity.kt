package network.meshway.android

import android.Manifest
import android.app.Activity
import android.content.Intent
import android.graphics.Color
import android.net.VpnService
import android.os.Build
import android.os.Bundle
import android.text.InputType
import android.view.View
import android.widget.*
import meshway.Meshway
import org.json.JSONObject
import java.io.File
import java.util.concurrent.Executors

class MainActivity : Activity() {
    private lateinit var state: TextView
    private lateinit var details: TextView
    private lateinit var config: EditText
    private lateinit var peer: EditText
    private lateinit var address: EditText
    private lateinit var route: EditText
    private lateinit var local: EditText
    private lateinit var vpn: Switch
    private lateinit var internet: Switch
    private var pendingServer: JSONObject? = null
    private lateinit var start: Button
    private lateinit var stop: Button
    private lateinit var save: Button
    private lateinit var serverList: LinearLayout
    private lateinit var catalogMessage: TextView
    private lateinit var accountDetails: TextView
    private lateinit var advanced: LinearLayout
    private val editable = mutableListOf<View>()
    private val background = Executors.newSingleThreadExecutor()
    private var replaceRecovery = false
    private val listener: (Snapshot) -> Unit = { render(it) }
    private val configFile get() = File(filesDir, "config.json")

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        val background = Color.rgb(245, 247, 250)
        val content = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; setPadding(40, 48, 40, 48); setBackgroundColor(background) }
        setContentView(ScrollView(this).apply { addView(content) })
        fun text(value: String, size: Float = 14f) = TextView(this).apply { text = value; textSize = size; setPadding(0, 12, 0, 12); content.addView(this) }
        fun field(label: String, value: String, multiline: Boolean = false): EditText {
            text(label)
            return EditText(this).apply {
                setText(value); setSingleLine(!multiline)
                inputType = InputType.TYPE_CLASS_TEXT or if (multiline) InputType.TYPE_TEXT_FLAG_MULTI_LINE else 0
                if (multiline) { minLines = 8; maxLines = 16; textSize = 12f; typeface = android.graphics.Typeface.MONOSPACE }
                content.addView(this); editable.add(this)
            }
        }
        fun button(label: String, action: () -> Unit) = Button(this).apply { text = label; setOnClickListener { action() }; content.addView(this) }
        text("Meshway", 32f)
        text("Your open peer network")
        state = text("Node stopped", 20f)
        details = text("").apply { setTextIsSelectable(true); textSize = 12f }
        val initial = try {
            if (configFile.exists()) configFile.readText() else Meshway.defaultConfig(File(filesDir, "node").path).also { configFile.writeText(it) }
        } catch (error: Exception) { details.text = "Configuration error: ${error.message}"; "{}" }
        start = button("Start peer node") { begin() }
        stop = button("Stop") { startService(Intent(this, MeshwayService::class.java).setAction(MeshwayService.STOP)) }
        text("Servers", 20f)
        internet = Switch(this).apply {
            text = "Protect all apps with VPN"
            isChecked = getPreferences(MODE_PRIVATE).getBoolean("internet", true)
            setOnCheckedChangeListener { _, enabled -> getPreferences(MODE_PRIVATE).edit().putBoolean("internet", enabled).apply() }
            content.addView(this)
        }
        text("Choose a server and connect. VPN sends app traffic and DNS through that server. Switch it off to use only the local SOCKS proxy.")
        catalogMessage = text("Start Meshway to see available servers.")
        serverList = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; content.addView(this) }
        button("Refresh servers") { if (NodeState.snapshot.running) startService(Intent(this, MeshwayService::class.java).setAction(MeshwayService.SERVERS)) else toast("Start Meshway first") }
        button("Disconnect") { if (NodeState.snapshot.running) startService(Intent(this, MeshwayService::class.java).setAction(MeshwayService.DISCONNECT)) }
        address = field("Add a server invitation · paste the owner's address", "")
        button("Add server") {
            if (NodeState.snapshot.running) startService(Intent(this, MeshwayService::class.java).setAction(MeshwayService.CONNECT).putExtra("address", address.text.toString().trim()))
            else toast("Start the peer node first")
        }
        text("Your local account", 19f)
        text("Your recovery key identifies your account. Each device has its own peer identity. History stays local unless sync is enabled and requested.")
        accountDetails = text("").apply { setTextIsSelectable(true); textSize = 12f }
        button("Refresh account and devices") { if (NodeState.snapshot.running) startService(Intent(this, MeshwayService::class.java).setAction(MeshwayService.ACCOUNT)) else toast("Start Meshway first") }
        val syncPeer = field("Account device peer ID for optional sync", "")
        button("Sync account with peer") { if (NodeState.snapshot.running) startService(Intent(this, MeshwayService::class.java).setAction(MeshwayService.SYNC).putExtra("peer", syncPeer.text.toString().trim())) else toast("Start Meshway first") }
        button("Save private recovery key…") {
            if (NodeState.snapshot.running || NodeState.snapshot.busy) { toast("Stop Meshway before saving your recovery key."); return@button }
            startActivityForResult(Intent(Intent.ACTION_CREATE_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("application/json").putExtra(Intent.EXTRA_TITLE, "meshway-recovery.json"), 200)
        }
        button("Import recovery key…") {
            if (NodeState.snapshot.running || NodeState.snapshot.busy) { toast("Stop Meshway before importing your recovery key."); return@button }
            fun choose(replace: Boolean) {
                replaceRecovery = replace
                startActivityForResult(Intent(Intent.ACTION_OPEN_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("*/*"), 201)
            }
            val statePath = try { JSONObject(configFile.readText()).getString("state_dir") } catch (_: Exception) { "" }
            if (File(statePath, "account.seed").exists()) {
                android.app.AlertDialog.Builder(this).setTitle("Replace your local account?")
                    .setMessage("Importing another recovery key replaces this account and clears local device/history records. Your peer identity stays the same. Save your current recovery key first if you need it.")
                    .setPositiveButton("Replace account") { _, _ -> choose(true) }.setNegativeButton("Cancel", null).show()
            } else choose(false)
        }
        text("Anyone with your recovery file can enroll devices in your account. Save it privately. Import joins an existing account while keeping this device's separate network identity.")
        button("Replace recovery key…") {
            if (NodeState.snapshot.running || NodeState.snapshot.busy) { toast("Stop Meshway and its VPN first."); return@button }
            android.app.AlertDialog.Builder(this).setTitle("Replace your recovery key?")
                .setMessage("This creates a new account ID and clears local device/history records. Save the new backup, then import it on every trusted device and personal server before reconnecting. The old key still controls the old account.")
                .setNegativeButton("Cancel", null).setPositiveButton("Choose new backup") { _, _ ->
                    startActivityForResult(Intent(Intent.ACTION_CREATE_DOCUMENT).addCategory(Intent.CATEGORY_OPENABLE).setType("application/json").putExtra(Intent.EXTRA_TITLE, "meshway-new-recovery.json"), 202)
                }.show()
        }
        val revokePeer = field("Device peer ID to revoke", "")
        button("Revoke device…") {
            if (!NodeState.snapshot.running) { toast("Start Meshway first."); return@button }
            val selected = revokePeer.text.toString().trim()
            if (selected.isEmpty()) { toast("Enter the device peer ID first."); return@button }
            android.app.AlertDialog.Builder(this).setTitle("Revoke this device?")
                .setMessage("The signed revocation reaches trusted devices when they synchronize. Rotate your account if its recovery key is compromised.")
                .setNegativeButton("Cancel", null).setPositiveButton("Revoke") { _, _ -> startService(Intent(this, MeshwayService::class.java).setAction(MeshwayService.REVOKE).putExtra("peer", selected)) }.show()
        }
        val synchronizeDeletion = android.widget.CheckBox(this).apply { text = "Apply history deletion to synced devices"; isChecked = true; content.addView(this) }
        button("Delete connection history…") {
            if (!NodeState.snapshot.running) { toast("Start Meshway first."); return@button }
            android.app.AlertDialog.Builder(this).setTitle("Delete connection history?")
                .setMessage(if (synchronizeDeletion.isChecked) "Create a signed history deletion that other devices apply on their next account sync?" else "Delete history only on this device? Other devices retain their copies.")
                .setNegativeButton("Cancel", null).setPositiveButton("Delete") { _, _ -> startService(Intent(this, MeshwayService::class.java).setAction(MeshwayService.CLEAR_HISTORY).putExtra("synchronize", synchronizeDeletion.isChecked)) }.show()
        }
        button("Advanced settings") { advanced.visibility = if (advanced.visibility == View.GONE) View.VISIBLE else View.GONE }
        val advancedStart = content.childCount
        text("Optional private IP tunnel", 19f)
        vpn = Switch(this).apply { text = "Enable Android VPN for the configured route"; content.addView(this); editable.add(this) }
        text("This separate mode links private subnets between devices. Use the server Connect buttons above for an internet VPN.")
        val preferences = getPreferences(MODE_PRIVATE)
        peer = field("VPN peer ID (required for outgoing packets)", preferences.getString("peer", "") ?: "")
        local = field("Local IPv4 address / prefix", preferences.getString("local", "10.77.0.2/30") ?: "10.77.0.2/30")
        route = field("Private IPv4 route / prefix", preferences.getString("route", "10.77.0.0/30") ?: "10.77.0.0/30")
        text("Full node configuration", 19f)
        text("Exit mode forwards traffic using your public IP and bandwidth. relay shares encrypted traffic. Both are off initially. Private destinations require allow_private_targets. Set allowed_peers to trusted peer IDs before serving an exit.")
        config = field("JSON · API tokens stay in app-private storage", JSONObject(initial).toString(2), true)
        val privateServer = Switch(this).apply { text = "Use this device as your private server"; content.addView(this); editable.add(this); isChecked = JSONObject(initial).optBoolean("personal_server") }
        privateServer.setOnCheckedChangeListener { _, enabled ->
            try {
                val json = JSONObject(config.text.toString()).put("personal_server", enabled)
                if (enabled) json.put("exit", true).put("advertise", true)
                config.setText(json.toString(2))
            } catch (error: Exception) { toast("Fix the JSON configuration before changing roles.") }
        }
        text("Your private server accepts devices enrolled with the same recovery key. Enabling it serves internet traffic using this device's IP address and bandwidth.")
        save = button("Save configuration") { try { saveConfiguration(); toast("Saved. Applies on next start.") } catch (error: Exception) { toast(error.message ?: "Invalid configuration") } }
        editable.add(save)
        text("SOCKS applications can use 127.0.0.1:1080 while the node runs. Bootstrap addresses or direct dialing are needed to find peers. Mobile operating systems may stop background traffic to save battery.")
        advanced = LinearLayout(this).apply { orientation = LinearLayout.VERTICAL; visibility = View.GONE }
        while (content.childCount > advancedStart) {
            val view = content.getChildAt(advancedStart)
            content.removeViewAt(advancedStart)
            advanced.addView(view)
        }
        content.addView(advanced)
        if (Build.VERSION.SDK_INT >= 33) requestPermissions(arrayOf(Manifest.permission.POST_NOTIFICATIONS), 101)
    }

    override fun onStart() { super.onStart(); NodeState.add(listener) }
    override fun onStop() { NodeState.remove(listener); super.onStop() }

    private fun render(snapshot: Snapshot) {
        if (!::state.isInitialized) return
        state.text = if (snapshot.busy) "Starting / stopping…" else if (snapshot.running) {
            if (snapshot.internetVpn) { if (snapshot.vpnConnected) "Internet VPN connected" else "Reconnecting · traffic blocked" }
            else if (snapshot.vpn) "Private IP tunnel active" else "Peer node running"
        } else "Node stopped"
        details.text = snapshot.message + if (snapshot.status.isNotEmpty()) "\n" + snapshot.status else ""
        start.isEnabled = !snapshot.running && !snapshot.busy
        start.text = if (snapshot.running) "Peer node running" else "Start Meshway"
        stop.isEnabled = snapshot.running && !snapshot.busy
        editable.forEach { it.isEnabled = !snapshot.running && !snapshot.busy }
        catalogMessage.text = if (snapshot.servers.isEmpty() || snapshot.servers == "[]") "No servers yet. Add an invitation from a friend or your server." else "Choose a server to connect."
        serverList.removeAllViews()
        try {
            val servers = org.json.JSONArray(snapshot.servers.ifEmpty { "[]" })
            for (index in 0 until servers.length()) {
                val server = servers.getJSONObject(index)
                val title = server.optString("label", "Peer server") + server.optString("country").let { if (it.isEmpty()) "" else " · $it" }
                serverList.addView(Button(this).apply {
                    text = "$title · Connect"
                    isEnabled = snapshot.running && !snapshot.busy && server.optBoolean("available") && server.optBoolean("can_connect")
                    setOnClickListener {
                        if (internet.isChecked) {
                            val permission = VpnService.prepare(this@MainActivity)
                            if (permission != null) { pendingServer = server; startActivityForResult(permission, 102); return@setOnClickListener }
                        }
                        selectServer(server)
                    }
                })
            }
        } catch (_: Exception) { catalogMessage.text = "Could not read server list." }
        accountDetails.text = snapshot.account
    }

    private fun saveConfiguration() {
        val json = JSONObject(config.text.toString())
        require(json.optString("state_dir").startsWith(filesDir.path + "/")) { "State directory must stay inside app storage." }
        require(!json.optBoolean("allow_private_targets") || json.optBoolean("exit")) { "Private destinations require the exit role." }
        // Go's config decoder performs the full validation when starting.
        val temp = File(filesDir, "config.json.tmp")
        temp.writeText(json.toString(2))
        require(temp.renameTo(configFile)) { "Could not save configuration" }
        getPreferences(MODE_PRIVATE).edit().putString("peer", peer.text.toString().trim()).putString("local", local.text.toString().trim()).putString("route", route.text.toString().trim()).apply()
    }

    private fun begin() {
        try {
            saveConfiguration()
            if (vpn.isChecked) {
                require(peer.text.toString().trim().isNotEmpty()) { "Set the remote VPN peer ID first." }
                val permission = VpnService.prepare(this)
                if (permission != null) { startActivityForResult(permission, 100); return }
            }
            startNode()
        } catch (error: Exception) { toast(error.message ?: "Could not start") }
    }

    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        super.onActivityResult(requestCode, resultCode, data)
        if (requestCode == 100 && resultCode == RESULT_OK) startNode()
        if (requestCode == 102) {
            val server = pendingServer
            pendingServer = null
            if (resultCode == RESULT_OK && server != null) selectServer(server)
            else toast("VPN permission was not granted.")
        }
        if (requestCode in 200..202 && resultCode == RESULT_OK && data?.data != null) {
            val uri = data.data!!
            val replace = replaceRecovery
            background.execute {
                try {
                    require(!NodeState.snapshot.running && !NodeState.snapshot.busy) { "Stop Meshway first." }
                    val temporary = File(filesDir, "recovery-transfer-${System.nanoTime()}.json")
                    try {
                        val stateDirectory = JSONObject(configFile.readText()).getString("state_dir")
                        if (requestCode == 200) {
                            // Start/stop creates the initial account using the real core, without opening listeners.
                            val json = JSONObject(configFile.readText()).put("listen_addresses", org.json.JSONArray()).put("socks_listen", "").put("api_listen", "").put("vpn", JSONObject().put("enabled", false))
                            val storageKey = StateVault.key(this)
                            Meshway.startSecure(json.toString(), storageKey).stop()
                            Meshway.exportRecoverySecure(stateDirectory, temporary.path, storageKey)
                            contentResolver.openOutputStream(uri, "wt")?.use { output -> temporary.inputStream().use { it.copyTo(output) } } ?: error("Could not open chosen file")
                        } else if (requestCode == 202) {
                            // Stage the new backup without changing this account.
                            // Commit only after Android's selected document was
                            // successfully written, so cancellation/I/O failures
                            // cannot rotate away the user's usable recovery key.
                            val staging = File(filesDir, "rotation-stage-${System.nanoTime()}")
                            try {
                                staging.mkdirs(); android.system.Os.chmod(staging.path, 0x1C0)
                                val key = StateVault.key(this)
                                Meshway.rotateRecoverySecure(staging.path, temporary.path, key)
                                contentResolver.openOutputStream(uri, "wt")?.use { output -> temporary.inputStream().use { it.copyTo(output) }; output.flush() } ?: error("Could not save the new recovery backup")
                                Meshway.importRecoverySecure(stateDirectory, temporary.path, true, key)
                            } finally { staging.deleteRecursively() }
                        } else {
                            contentResolver.openInputStream(uri)?.use { input ->
                                val bytes = input.readNBytes(8193)
                                require(bytes.size <= 8192) { "Recovery file is too large." }
                                temporary.writeBytes(bytes)
                                android.system.Os.chmod(temporary.path, 0x180) // 0600, required by core private-file validation.
                            } ?: error("Could not read chosen file")
                            Meshway.importRecoverySecure(stateDirectory, temporary.path, replace, StateVault.key(this))
                        }
                    } finally { temporary.delete() }
                    runOnUiThread { toast(if (requestCode == 200) "Recovery key saved. Keep it private." else if (requestCode == 202) "New recovery key saved. Import it on every trusted device and private server; your account ID changed." else "Account imported. Start Meshway to enroll this device.") }
                } catch (error: Exception) { runOnUiThread { toast(error.message ?: "Recovery transfer failed") } }
            }
        }
    }

    private fun startNode() {
        val intent = Intent(this, MeshwayService::class.java).setAction(MeshwayService.START)
            .putExtra("vpn", vpn.isChecked).putExtra("peer", peer.text.toString().trim())
            .putExtra("local", local.text.toString().trim()).putExtra("route", route.text.toString().trim())
        startForegroundService(intent)
    }
    private fun selectServer(server: JSONObject) {
        startService(Intent(this, MeshwayService::class.java).setAction(MeshwayService.SELECT)
            .putExtra("peer", server.getString("peer_id")).putExtra("address", server.optString("address"))
            .putExtra("internet", internet.isChecked))
    }
    private fun toast(message: String) = Toast.makeText(this, message, Toast.LENGTH_LONG).show()
}
