package com.slacker.android;

import android.net.VpnService;

/**
 * Reserved for the future native packet transport.
 * Never establish a TUN without a working forwarding engine:
 * doing so would silently blackhole all device traffic.
 */
public final class SlackerVpnService extends VpnService {
    @Override public void onRevoke() {
        stopSelf();
        super.onRevoke();
    }
}
