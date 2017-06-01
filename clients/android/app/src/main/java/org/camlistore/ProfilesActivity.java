/*
Copyright 2017 The Camlistore Authors.

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

import java.util.HashSet;
import java.util.Set;

import android.content.Context;
import android.content.Intent;
import android.content.SharedPreferences;
import android.content.SharedPreferences.Editor;
import android.os.Bundle;
import android.preference.ListPreference;
import android.preference.EditTextPreference;
import android.preference.Preference;
import android.preference.Preference.OnPreferenceChangeListener;
import android.preference.PreferenceActivity;
import android.util.Log;

public class ProfilesActivity extends PreferenceActivity {
    private static final String TAG = "ProfilesActivity";
    private ListPreference mProfilePref;
    private EditTextPreference mNewProfilePref;
    private SharedPreferences mSharedPrefs;


    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        mSharedPrefs = getSharedPreferences(Preferences.PROFILES_FILE, 0);
        getPreferenceManager().setSharedPreferencesName(Preferences.PROFILES_FILE);
        // In effect, I think the default values from arrays.xml are useless since we
        // always override them with refreshProfileRef right after.
        addPreferencesFromResource(R.xml.profiles);
        mProfilePref = (ListPreference) findPreference(Preferences.PROFILE);
        refreshProfileRef();
        mNewProfilePref = (EditTextPreference) findPreference(Preferences.NEWPROFILE);

        OnPreferenceChangeListener onChange = new OnPreferenceChangeListener() {
            @Override
            public boolean onPreferenceChange(Preference pref, Object newValue) {
                // Note: newValue isn't yet persisted, but easiest to update the
                // UI here.
                if (!(newValue instanceof String)) {
                    return false;
                }
                String newStr = (String) newValue;
                if (pref == mProfilePref) {
                    updateProfilesSummary(newStr);
                } else if (pref == mNewProfilePref) {
                    updateProfilesList(newStr);
                    return false; // do not actually persist it.
                }
                // TODO(mpl): some way to remove a profile.
                return true; // yes, persist it
            }
        };
        mProfilePref.setOnPreferenceChangeListener(onChange);
        mNewProfilePref.setOnPreferenceChangeListener(onChange);
   }

    @Override
    protected void onResume() {
        super.onResume();
        refreshProfileRef();
        updatePreferenceSummaries();
    }

    private void updatePreferenceSummaries() {
        updateProfilesSummary(mProfilePref.getValue());
    }

    private void updateProfilesSummary(String value) {
        if (value == null || value.isEmpty()) {
            return;
        }
        mProfilePref.setSummary(value);
    }

    // updateProfilesList adds value to the set of existing profiles to the
    // key/value store, and refreshes the preference list.
    private void updateProfilesList(String value) {
        if (value == null || value.isEmpty()) {
            return;
        }
        String removedChars = "(%|\\?|:|\"|\\*|\\||/|\\|<|>| )";
        value = value.replaceAll(removedChars, "");
        if (value.isEmpty()) {
            return;
        }

        Set<String> profiles = mSharedPrefs.getStringSet(Preferences.PROFILES, new HashSet<String>());
        profiles.add(value);
        Editor ed = mSharedPrefs.edit();
        ed.putStringSet(Preferences.PROFILES, profiles);
        ed.commit();
        refreshProfileRef();
        mProfilePref.setValue(value);
        mProfilePref.setSummary(value);
        Log.v(TAG, "New profile added: " + value);
    }

    // refreshProfileRef refreshes the profiles preference list with the profile
    // values stored in the key/value file.
    private void refreshProfileRef() {
        Set<String> profiles = mSharedPrefs.getStringSet(Preferences.PROFILES, new HashSet<String>());
        if (profiles.isEmpty()) {
            // make sure there's always at least the "default" profile.
            profiles.add("default");
            Editor ed = mSharedPrefs.edit();
            ed.putStringSet(Preferences.PROFILES, profiles);
            ed.commit();
        }
        CharSequence[] listValues = profiles.toArray(new String[]{});
        mProfilePref.setEntries(listValues);
        mProfilePref.setEntryValues(listValues);
    }

    // Convenience method.
    static void show(Context context) {
        final Intent intent = new Intent(context, ProfilesActivity.class);
        context.startActivity(intent);
    }
}
