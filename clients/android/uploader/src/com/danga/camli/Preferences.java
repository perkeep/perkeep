package com.danga.camli;

import android.content.SharedPreferences;

public final class Preferences {
    public static final String NAME = "CamliUploader";

    public static final String HOST = "camli.host";
    public static final String PASSWORD = "camli.password";
    public static final String AUTO = "camli.auto";
    public static final String AUTO_OPTS = "camli.auto.opts";

    public static final String AUTO_REQUIRE_POWER = "camli.auto.require_power";
    public static final String AUTO_REQUIRE_WIFI = "camli.auto.require_wifi";

    public static final String AUTO_DIR_PHOTOS = "camli.auto.photos";
    public static final String AUTO_DIR_MYTRACKS = "camli.auto.mytracks";

    public boolean autoRequiresPower() {
        return mSP.getBoolean(AUTO_REQUIRE_POWER, false);
    }

    public boolean autoUpload() {
        return mSP.getBoolean(AUTO, false);
    }

    private final SharedPreferences mSP;

    public Preferences(SharedPreferences prefs) {
        mSP = prefs;
    }
}
