// GoMySQL - A MySQL client library for Go
//
// Copyright 2010-2011 Phil Bayfield. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.
package mysql

import "fmt"

// Date struct
type Date struct {
	Year  uint16
	Month uint8
	Day   uint8
}

// Get date as string
func (d *Date) String() string {
	return fmt.Sprintf("%d-%02d-%02d", d.Year, d.Month, d.Day)
}

// Time struct
type Time struct {
	Hour   uint8
	Minute uint8
	Second uint8
}

// Get time as string
func (t *Time) String() string {
	return fmt.Sprintf("%02d:%02d:%02d", t.Hour, t.Minute, t.Second)
}

// DateTime struct
type DateTime struct {
	Year   uint16
	Month  uint8
	Day    uint8
	Hour   uint8
	Minute uint8
	Second uint8
}

// Get date/time as string
func (d *DateTime) String() string {
	return fmt.Sprintf("%d-%02d-%02d %02d:%02d:%02d", d.Year, d.Month, d.Day, d.Hour, d.Minute, d.Second)
}
