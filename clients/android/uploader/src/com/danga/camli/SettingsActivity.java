package com.danga.camli;

import android.content.ComponentName;
import android.content.Context;
import android.content.Intent;
import android.content.ServiceConnection;
import android.os.Bundle;
import android.os.IBinder;
import android.os.RemoteException;
import android.preference.CheckBoxPreference;
import android.preference.EditTextPreference;
import android.preference.Preference;
import android.preference.PreferenceActivity;
import android.preference.PreferenceScreen;
import android.preference.Preference.OnPreferenceChangeListener;
import android.util.Log;

public class SettingsActivity extends PreferenceActivity {
    private static final String TAG = "SettingsActivity";

    private IUploadService mServiceStub = null;

    private EditTextPreference hostPref;
    private EditTextPreference passwordPref;
    private CheckBoxPreference autoPref;
    private PreferenceScreen autoOpts;

    private final ServiceConnection mServiceConnection = new ServiceConnection() {
        public void onServiceConnected(ComponentName name, IBinder service) {
            mServiceStub = IUploadService.Stub.asInterface(service);
        }

        public void onServiceDisconnected(ComponentName name) {
            mServiceStub = null;
        };
    };

    @Override
    protected void onCreate(Bundle savedInstanceState) {
        super.onCreate(savedInstanceState);

        getPreferenceManager().setSharedPreferencesName(Preferences.NAME);
        addPreferencesFromResource(R.xml.preferences);

        hostPref = (EditTextPreference) findPreference(Preferences.HOST);
        passwordPref = (EditTextPreference) findPreference(Preferences.PASSWORD);
        autoPref = (CheckBoxPreference) findPreference(Preferences.AUTO);
        autoOpts = (PreferenceScreen) findPreference(Preferences.AUTO_OPTS);

        OnPreferenceChangeListener onChange = new OnPreferenceChangeListener() {
            public boolean onPreferenceChange(Preference pref, Object newValue) {
                final String key = pref.getKey();
                Log.v(TAG, "preference change for: " + key);

                // Note: newValue isn't yet persisted, but easiest to update the
                // UI here.
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
        
        autoPref.setOnPreferenceChangeListener(new OnPreferenceChangeListener() {
            public boolean onPreferenceChange(Preference preference, Object newObjValue) {
                Boolean newValue = (Boolean) newObjValue;
                updateAutoOpts(newValue);
                if (mServiceStub != null) {
                    try {
                        mServiceStub.setBackgroundWatchersEnabled(newValue.booleanValue());
                    } catch (RemoteException e) {
                        // Ignore.
                    }
                }
                return true; // yes, persist it.
            }
        });
    }

    @Override
    protected void onPause() {
        super.onPause();
        if (mServiceConnection != null) {
            unbindService(mServiceConnection);
        }
    }

    @Override
    protected void onResume() {
        super.onResume();
        updatePreferenceSummaries();
        bindService(new Intent(this, UploadService.class), mServiceConnection,
                Context.BIND_AUTO_CREATE);
    }

    private void updatePreferenceSummaries() {
        updateHostSummary(hostPref.getText());
        updatePasswordSummary(passwordPref.getText());
        updateAutoOpts(autoPref.isChecked());
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

    private void updateAutoOpts(boolean checked) {
        autoOpts.setEnabled(checked);
    }

    // Convenience method.
    static void show(Context context) {
        final Intent intent = new Intent(context, SettingsActivity.class);
        context.startActivity(intent);
    }
}
