package com.danga.camli;

import android.test.ActivityInstrumentationTestCase2;

public class CamliActivityTest extends ActivityInstrumentationTestCase2<CamliActivity> {
	
	public CamliActivityTest(String pkg, Class<CamliActivity> activityClass) {
		super(pkg, activityClass);
		// TODO Auto-generated constructor stub
	}

	public void testSanity() {
		assertEquals(2, 1 + 1);
		assertEquals(4, 2 + 2);
	}
}
