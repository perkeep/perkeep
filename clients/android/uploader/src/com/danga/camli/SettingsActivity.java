package com.danga.camli;

import android.content.Context;
import android.content.Intent;
import android.os.Bundle;
import android.preference.EditTextPreference;
import android.preference.Preference;
import android.preference.PreferenceActivity;
import android.preference.Preference.OnPreferenceChangeListener;
import android.util.Log;

public class SettingsActivity extends PreferenceActivity {
    private static final String TAG = "SettingsActivity";

    private EditTextPreference hostPref;
    private EditTextPreference passwordPref;

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        getPreferenceManager().setSharedPreferencesName(Preferences.NAME);
        addPreferencesFromResource(R.xml.preferences);

        hostPref = (EditTextPreference) findPreference(Preferences.HOST);
        passwordPref = (EditTextPreference) findPreference(Preferences.PASSWORD);

        OnPreferenceChangeListener onChange = new OnPreferenceChangeListener() {
            public boolean onPreferenceChange(Preference pref, Object newValue) {
                final String key = pref.getKey();
                Log.v(TAG, "preference change for: " + key);

                // Note: newValue isn't yet persisted, but easiest to update the
                // UI
                // here.
                String newStr = (newValue instanceof String) ? (String) newValue
                        : null;
                if (pref == hostPref) {
                    updateHostSummary(newStr);
                }
                if (pref == passwordPref) {
                    updatePasswordSummary(newStr);
                }
                return true; // yes, persist it
                }
        };
        hostPref.setOnPreferenceChangeListener(onChange);
        passwordPref.setOnPreferenceChangeListener(onChange);
    }

    @Override
    protected void onResume() {
        super.onResume();
        updatePreferenceSummaries();
    }

    private void updatePreferenceSummaries() {
        updateHostSummary(hostPref.getText());
        updatePasswordSummary(passwordPref.getText());
    }

    private void updatePasswordSummary(String value) {
        if (value != null && value.length() > 0) {
            passwordPref.setSummary("*********");
        } else {
            passwordPref.setSummary("<unset>");
            }
        }

    private void updateHostSummary(String value) {
        if (value != null && value.length() > 0) {
            hostPref.setSummary(value);
        } else {
            hostPref.setSummary(getString(R.string.settings_host_summary));
        }
    }

    // Convenience method.
    static void show(Context context) {
        final Intent intent = new Intent(context, SettingsActivity.class);
        context.startActivity(intent);
    }
}
