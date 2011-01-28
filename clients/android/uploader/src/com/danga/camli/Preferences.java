/*
Copyright 2011 Google Inc.

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
