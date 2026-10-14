package network.meshway.android

import android.content.Context
import android.security.keystore.KeyGenParameterSpec
import android.security.keystore.KeyProperties
import java.io.File
import java.security.KeyStore
import java.security.SecureRandom
import javax.crypto.Cipher
import javax.crypto.KeyGenerator
import javax.crypto.SecretKey
import javax.crypto.spec.GCMParameterSpec

/** A device-only Keystore key wraps the random key passed to the Go state vault. */
object StateVault {
    private const val ALIAS = "network.meshway.state-vault-v1"
    @Synchronized fun key(context: Context): ByteArray {
        val store = KeyStore.getInstance("AndroidKeyStore").apply { load(null) }
        val file = File(context.filesDir, "state-key.wrapped")
        if (file.exists() && !store.containsAlias(ALIAS)) error("Device storage key is unavailable. Restore your recovery backup on a new account.")
        val wrapping = if (store.containsAlias(ALIAS)) store.getKey(ALIAS, null) as SecretKey else {
            KeyGenerator.getInstance(KeyProperties.KEY_ALGORITHM_AES, "AndroidKeyStore").apply {
                init(KeyGenParameterSpec.Builder(ALIAS, KeyProperties.PURPOSE_ENCRYPT or KeyProperties.PURPOSE_DECRYPT)
                    .setBlockModes(KeyProperties.BLOCK_MODE_GCM).setEncryptionPaddings(KeyProperties.ENCRYPTION_PADDING_NONE)
                    .setKeySize(256).setRandomizedEncryptionRequired(true).build())
            }.generateKey()
        }
        val cipher = Cipher.getInstance("AES/GCM/NoPadding")
        if (file.exists()) {
            val bytes = file.readBytes()
            require(bytes.size == 61 && bytes[0] == 1.toByte()) { "Invalid device storage key file." }
            cipher.init(Cipher.DECRYPT_MODE, wrapping, GCMParameterSpec(128, bytes.copyOfRange(1, 13)))
            return cipher.doFinal(bytes.copyOfRange(13, bytes.size)).also { require(it.size == 32) }
        }
        val key = ByteArray(32).also { SecureRandom().nextBytes(it) }
        cipher.init(Cipher.ENCRYPT_MODE, wrapping)
        val bytes = byteArrayOf(1) + cipher.iv + cipher.doFinal(key)
        val temporary = File(context.filesDir, "state-key.wrapped.tmp")
        temporary.outputStream().use { output -> output.write(bytes); output.flush() }
        android.system.Os.chmod(temporary.path, 0x180)
        require(temporary.renameTo(file)) { "Could not save device storage key." }
        return key
    }
}
