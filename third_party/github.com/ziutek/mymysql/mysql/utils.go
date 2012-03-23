package mysql

// This call Start and next call GetRow as long as it reads all rows from the
// result. Next it returns all readed rows as the slice of rows.
func Query(c Conn, sql string, params ...interface{}) (rows []Row, res Result, err error) {
	res, err = c.Start(sql, params...)
	if err != nil {
		return
	}
	rows, err = GetRows(res)
	return
}

// This call Run and next call GetRow as long as it reads all rows from the
// result. Next it returns all readed rows as the slice of rows.
func Exec(s Stmt, params ...interface{}) (rows []Row, res Result, err error) {
	res, err = s.Run(params...)
	if err != nil {
		return
	}
	rows, err = GetRows(res)
	return
}

// Read all unreaded rows and discard them. This function is useful if you
// don't want to use the remaining rows. It has an impact only on current
// result. If there is multi result query, you must use NextResult method and
// read/discard all rows in this result, before use other method that sends
// data to the server. You can't use this function if last GetRow returned nil.
func End(r Result) (err error) {
	var row Row
	for {
		row, err = r.GetRow()
		if err != nil || row == nil {
			break
		}
	}
	return
}

// Reads all rows from result and returns them as slice.
func GetRows(r Result) (rows []Row, err error) {
	var row Row
	for {
		row, err = r.GetRow()
		if err != nil || row == nil {
			break
		}
		rows = append(rows, row)
	}
	return
}
