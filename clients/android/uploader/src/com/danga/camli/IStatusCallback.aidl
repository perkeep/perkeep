package com.danga.camli;

oneway interface IStatusCallback {
    void logToClient(String stuff);
    void onUploadStatusChange(boolean uploading);
}
