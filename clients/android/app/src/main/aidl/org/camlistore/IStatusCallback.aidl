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

package org.camlistore;

oneway interface IStatusCallback {
    void logToClient(String stuff);
    void setUploadStatusText(String text); // single line
    void setUploadStatsText(String text);  // big box
    void setUploading(boolean uploading);
    
    // done: acknowledged by server
    // inFlight: those written to the server, but no reply yet (i.e. large HTTP POST body) (does NOT include the "done" ones)
    // total: "this batch" size.  reset on transition from 0 -> 1 blobs remain.
    void setFileStatus(int done, int inFlight, int total);
    void setByteStatus(long done, int inFlight, long total);
}
