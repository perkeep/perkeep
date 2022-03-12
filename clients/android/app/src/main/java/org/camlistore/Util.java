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

import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;

public class Util {
    private static final int NUM_THREADS = 4;
    private static final ExecutorService executor = Executors.newFixedThreadPool(NUM_THREADS);

    public static void runAsync(final Runnable r) {
        executor.execute(r);
    }
}
