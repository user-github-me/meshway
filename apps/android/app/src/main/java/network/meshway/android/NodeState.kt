package network.meshway.android

import android.os.Handler
import android.os.Looper
import java.util.concurrent.CopyOnWriteArraySet

data class Snapshot(val running: Boolean = false, val busy: Boolean = false, val vpn: Boolean = false, val status: String = "", val message: String = "Node stopped", val servers: String = "[]", val account: String = "", val internetVpn: Boolean = false, val vpnConnected: Boolean = false)

object NodeState {
    @Volatile var snapshot = Snapshot()
        private set
    private val listeners = CopyOnWriteArraySet<(Snapshot) -> Unit>()
    private val main = Handler(Looper.getMainLooper())
    fun update(value: Snapshot) { snapshot = value; main.post { listeners.forEach { it(value) } } }
    fun add(listener: (Snapshot) -> Unit) { listeners.add(listener); listener(snapshot) }
    fun remove(listener: (Snapshot) -> Unit) { listeners.remove(listener) }
}
