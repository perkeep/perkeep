package com.danga.camli;

oneway interface IStatusCallback {
    void logToClient(String stuff);
    void setUploadStatusText(String text);
    void setUploading(boolean uploading);
    
    void setBlobsRemain(int toUpload, int toDigest);

    // done: acknowledged by server
    // inFlight: written to the server, but no reply yet (i.e. large HTTP POST body)
    // total: "this batch" size.  reset on transition from 0 -> 1 blobs remain.
    void setBlobStatus(int done, int inFlight, int total);
    void setByteStatus(long done, int inFlight, long total);
}
