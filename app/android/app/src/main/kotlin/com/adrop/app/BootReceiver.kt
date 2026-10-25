package com.adrop.app

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent

/** Starts receiving after a reboot when "Receive when the app is closed" is on. */
class BootReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action == Intent.ACTION_BOOT_COMPLETED && Core.setting(context, "background")) {
            AdropService.start(context)
        }
    }
}
