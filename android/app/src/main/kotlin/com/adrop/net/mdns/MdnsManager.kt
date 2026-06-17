package com.adrop.net.mdns

import android.content.Context
import android.net.nsd.NsdManager
import android.net.nsd.NsdServiceInfo
import android.os.Build
import android.util.Log
import com.adrop.data.trust.TrustRepository
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.launch

/**
 * Manages mDNS/DNS-SD registration and discovery for _adrop._tcp using NsdManager.
 *
 * Register/unregister controls our own service advertisement (while the receive window
 * is open). startDiscovery/stopDiscovery can run independently to keep peer addresses
 * up-to-date at any time.
 *
 * Security note: mDNS is only used to update the stored address of already-paired
 * devices. All connections still require pinned mTLS, so a spoofed mDNS record
 * can only cause a failed connection attempt.
 */
class MdnsManager(private val context: Context, private val trustRepo: TrustRepository) {

    private val nsdManager = context.getSystemService(Context.NSD_SERVICE) as NsdManager
    private val scope = CoroutineScope(Dispatchers.IO)

    private var registrationListener: NsdManager.RegistrationListener? = null
    private var discoveryListener: NsdManager.DiscoveryListener? = null

    companion object {
        private const val TAG = "MdnsManager"
        private const val SERVICE_TYPE = "_adrop._tcp"
    }

    // ---------------------------------------------------------------------------
    // Registration (advertise our own service)
    // ---------------------------------------------------------------------------

    fun register(deviceName: String, port: Int, fingerprint: String) {
        val info = NsdServiceInfo().apply {
            serviceName = deviceName
            serviceType = SERVICE_TYPE
            this.port = port
            if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                setAttribute("fp", fingerprint)
            }
        }

        val listener = object : NsdManager.RegistrationListener {
            override fun onRegistrationFailed(info: NsdServiceInfo, errorCode: Int) {
                Log.w(TAG, "registration failed: $errorCode")
            }
            override fun onUnregistrationFailed(info: NsdServiceInfo, errorCode: Int) {
                Log.w(TAG, "unregistration failed: $errorCode")
            }
            override fun onServiceRegistered(info: NsdServiceInfo) {
                Log.i(TAG, "registered as ${info.serviceName}")
            }
            override fun onServiceUnregistered(info: NsdServiceInfo) {
                Log.i(TAG, "unregistered ${info.serviceName}")
            }
        }

        registrationListener = listener
        try {
            nsdManager.registerService(info, NsdManager.PROTOCOL_DNS_SD, listener)
        } catch (e: Exception) {
            Log.e(TAG, "registerService failed: ${e.message}")
        }
    }

    fun unregister() {
        val listener = registrationListener ?: return
        registrationListener = null
        try {
            nsdManager.unregisterService(listener)
        } catch (e: Exception) {
            Log.e(TAG, "unregisterService failed: ${e.message}")
        }
    }

    // ---------------------------------------------------------------------------
    // Discovery (find peers and update their stored addresses)
    // ---------------------------------------------------------------------------

    fun startDiscovery() {
        val listener = object : NsdManager.DiscoveryListener {
            override fun onDiscoveryStarted(serviceType: String) {
                Log.d(TAG, "discovery started")
            }
            override fun onDiscoveryStopped(serviceType: String) {
                Log.d(TAG, "discovery stopped")
            }
            override fun onStartDiscoveryFailed(serviceType: String, errorCode: Int) {
                Log.w(TAG, "start discovery failed: $errorCode")
                discoveryListener = null
            }
            override fun onStopDiscoveryFailed(serviceType: String, errorCode: Int) {
                Log.w(TAG, "stop discovery failed: $errorCode")
            }
            override fun onServiceFound(info: NsdServiceInfo) {
                resolveService(info)
            }
            override fun onServiceLost(info: NsdServiceInfo) {
                Log.d(TAG, "service lost: ${info.serviceName}")
            }
        }

        discoveryListener = listener
        try {
            nsdManager.discoverServices(SERVICE_TYPE, NsdManager.PROTOCOL_DNS_SD, listener)
        } catch (e: Exception) {
            Log.e(TAG, "discoverServices failed: ${e.message}")
            discoveryListener = null
        }
    }

    fun stopDiscovery() {
        val listener = discoveryListener ?: return
        discoveryListener = null
        try {
            nsdManager.stopServiceDiscovery(listener)
        } catch (e: Exception) {
            Log.e(TAG, "stopServiceDiscovery failed: ${e.message}")
        }
    }

    // ---------------------------------------------------------------------------
    // Internal helpers
    // ---------------------------------------------------------------------------

    private fun resolveService(info: NsdServiceInfo) {
        nsdManager.resolveService(info, object : NsdManager.ResolveListener {
            override fun onResolveFailed(info: NsdServiceInfo, errorCode: Int) {
                Log.d(TAG, "resolve failed for ${info.serviceName}: $errorCode")
            }

            override fun onServiceResolved(info: NsdServiceInfo) {
                val host = info.host?.hostAddress ?: return
                val port = info.port
                val addr = "$host:$port"

                // On API 33+ we can read the fp= TXT attribute directly.
                // On older APIs fall back to matching by service name.
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                    val fpBytes = info.attributes["fp"] ?: run {
                        matchByName(info.serviceName, addr)
                        return
                    }
                    val fp = String(fpBytes, Charsets.UTF_8)
                    updatePeerAddr(fp, addr)
                } else {
                    matchByName(info.serviceName, addr)
                }
            }
        })
    }

    private fun updatePeerAddr(fingerprint: String, addr: String) {
        scope.launch {
            val device = trustRepo.findByFingerprint(fingerprint) ?: return@launch
            trustRepo.updateAddr(fingerprint, addr)
            Log.i(TAG, "mDNS: updated addr for ${device.name} to $addr")
        }
    }

    private fun matchByName(serviceName: String, addr: String) {
        scope.launch {
            val devices = trustRepo.getAll()
            val match = devices.firstOrNull { it.name == serviceName } ?: return@launch
            trustRepo.updateAddr(match.fingerprint, addr)
            Log.i(TAG, "mDNS: updated addr for ${match.name} to $addr (matched by name)")
        }
    }
}
