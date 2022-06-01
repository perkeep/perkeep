/*
Copyright 2011 The Perkeep Authors

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

     http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
 */

package org.camlistore;

import android.content.Context;
import android.content.SharedPreferences;

public final class Preferences {
    public static final String NAME = "perkeepUploader";

	// key/value store file where we keep the profile names
    public static final String PROFILES_FILE = "perkeepUploader_profiles";
	// key to the set of profile names
    public static final String PROFILES = "perkeep.profiles";
	// key to the currently selected profile
    public static final String PROFILE = "perkeep.profile";
	// for the preference element that lets us create a new profile name
    public static final String NEWPROFILE = "perkeep.newprofile";

    public static final String HOST = "perkeep.host";
    // TODO(mpl): list instead of single string later? seems overkill for now.
    public static final String USERNAME = "perkeep.username";
    public static final String PASSWORD = "perkeep.password";
    public static final String AUTO = "perkeep.auto";
    public static final String AUTO_OPTS = "perkeep.auto.opts";
    public static final String MAX_CACHE_MB = "perkeep.max_cache_mb";
    public static final String DEV_IP = "perkeep.dev_ip";
    public static final String AUTO_REQUIRE_POWER = "perkeep.auto.require_power";
    public static final String AUTO_REQUIRE_WIFI = "perkeep.auto.require_wifi";
    public static final String AUTO_REQUIRED_WIFI_SSID = "perkeep.auto.required_wifi_ssid";
    public static final String AUTO_DIR_PHOTOS = "perkeep.auto.photos";
    public static final String AUTO_DIR_MYTRACKS = "perkeep.auto.mytracks";

    private final SharedPreferences mSP;

    public Preferences(SharedPreferences prefs) {
        mSP = prefs;
    }

    // filename returns the settings file name for the currently selected profile.
    public static String filename(Context ctx) {
        SharedPreferences profiles = ctx.getSharedPreferences(PROFILES_FILE, 0);
        String currentProfile = profiles.getString(Preferences.PROFILE, "default");
        if (currentProfile.equals("default")) {
            // Special case: we keep perkeepUploader as the conf file name by default, to stay
            // backwards compatible.
            return NAME;
        }
        return NAME+"."+currentProfile;
    }

    public boolean autoRequiresPower() {
        return mSP.getBoolean(AUTO_REQUIRE_POWER, false);
    }

    public boolean autoRequiresWifi() {
        return mSP.getBoolean(AUTO_REQUIRE_WIFI, false);
    }

    public String autoRequiredWifiSSID() {
        return mSP.getString(AUTO_REQUIRED_WIFI_SSID, "");
    }

    public boolean autoUpload() {
        return mSP.getBoolean(AUTO, false);
    }

    public int maxCacheMb() {
        return Integer.parseInt(mSP.getString(MAX_CACHE_MB, "256"));
    }

    public boolean autoDirPhotos() {
        return mSP.getBoolean(AUTO_DIR_PHOTOS, true);
    }

    public boolean autoDirMyTracks() {
        return mSP.getBoolean(AUTO_DIR_MYTRACKS, true);
    }

    private String devIP() {
        return mSP.getString(DEV_IP, "");
    }

    private boolean inDevMode() {
        return !devIP().isEmpty();
    }

    public String username() {
        if (inDevMode()) {
            return "perkeep";
        }
        return mSP.getString(USERNAME, "");
    }

    public String password() {
        if (inDevMode()) {
            return "pass3179";
        }
        return mSP.getString(PASSWORD, "");
    }

    public HostPort hostPort() {
        if (inDevMode()) {
            return new HostPort("http://" + devIP() + ":3179");
        }
        return new HostPort(mSP.getString(Preferences.HOST, ""));
    }

    public void setDevIP(String value) {
        mSP.edit().putString(DEV_IP, value).apply();
    }

}
