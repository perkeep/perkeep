package com.danga.camli;

oneway interface IStatusCallback {
    void logToClient(String stuff);
    void onUploadStatusChange(boolean uploading);

    void setBlobsRemain(int num);
    void setBlobStatus(int done, int total);
    void setByteStatus(long done, long total);
}
