/**
 * Background delivery of queued ([OutboxStore]) sends.
 *
 * Runs under WorkManager so it survives the Send screen closing, app death and
 * device reboot, and is gated on network connectivity. It is enqueued when:
 *   - a send fails because the PC was offline (immediate, optimistic attempt),
 *   - the target PC reappears on the LAN via mDNS ([com.adrop.net.mdns.MdnsManager]),
 *   - the app starts ([com.adrop.AdropApplication]).
 *
 * The transfer protocol is manifest/hash based and resumable, so re-attempting
 * a partially delivered item is safe — the daemon dedupes by content.
 */
package com.adrop.feature.send

import android.content.Context
import androidx.work.*
import com.adrop.data.trust.TrustRepository
import com.adrop.net.tls.PinningTrustManager

class SendWorker(
    appContext: Context,
    params: WorkerParameters,
) : CoroutineWorker(appContext, params) {

    override suspend fun doWork(): Result {
        val outbox    = OutboxStore.getInstance(applicationContext)
        val trustRepo = TrustRepository.getInstance(applicationContext)

        // If a specific fingerprint was requested (mDNS saw that PC), flush only
        // that target; otherwise flush everything.
        val onlyFp = inputData.getString(KEY_FINGERPRINT)
        val items  = if (onlyFp != null) outbox.forTarget(onlyFp) else outbox.getAll()
        if (items.isEmpty()) return Result.success()

        val devices  = trustRepo.getAll()
        val trustMgr = PinningTrustManager(isTrusted = { fp -> devices.find { it.fingerprint == fp } })

        var anyFailed = false
        for (item in items) {
            val device = devices.find { it.fingerprint == item.targetFingerprint }
            if (device == null) {
                // Device was unpaired while queued — drop the orphaned item.
                outbox.remove(item)
                continue
            }
            val delivered = runCatching {
                when (item.kind) {
                    OutboxKind.FILES -> {
                        val files = outbox.payloadFiles(item)
                        if (files.isNotEmpty()) {
                            sendFilesNow(
                                applicationContext, device, files, trustMgr,
                                displayNames = files.map { outbox.displayName(it) },
                            )
                        }
                    }
                    OutboxKind.CLIPBOARD -> {
                        val bytes = outbox.clipboardBlob(item).readBytes()
                        sendClipboardNow(applicationContext, device, bytes, item.mime, trustMgr)
                    }
                }
            }.isSuccess

            if (delivered) outbox.remove(item) else anyFailed = true
        }

        // If something is still undelivered the PC is presumably still offline;
        // ask WorkManager to retry later with backoff.
        return if (anyFailed) Result.retry() else Result.success()
    }

    companion object {
        const val KEY_FINGERPRINT = "fingerprint"
        private const val UNIQUE_FLUSH_ALL = "adrop_outbox_flush"

        private fun constraints() = Constraints.Builder()
            .setRequiredNetworkType(NetworkType.CONNECTED)
            .build()

        /** Flush every queued item (e.g. on app start). */
        fun enqueueAll(context: Context) {
            val req = OneTimeWorkRequestBuilder<SendWorker>()
                .setConstraints(constraints())
                .setBackoffCriteria(BackoffPolicy.LINEAR, 30, java.util.concurrent.TimeUnit.SECONDS)
                .build()
            WorkManager.getInstance(context)
                .enqueueUniqueWork(UNIQUE_FLUSH_ALL, ExistingWorkPolicy.REPLACE, req)
        }

        /** Flush only items targeting [fingerprint] (e.g. it just reappeared on mDNS). */
        fun enqueueForTarget(context: Context, fingerprint: String) {
            val req = OneTimeWorkRequestBuilder<SendWorker>()
                .setConstraints(constraints())
                .setInputData(workDataOf(KEY_FINGERPRINT to fingerprint))
                .setBackoffCriteria(BackoffPolicy.LINEAR, 30, java.util.concurrent.TimeUnit.SECONDS)
                .build()
            WorkManager.getInstance(context)
                .enqueueUniqueWork("adrop_outbox_flush_$fingerprint", ExistingWorkPolicy.REPLACE, req)
        }
    }
}
