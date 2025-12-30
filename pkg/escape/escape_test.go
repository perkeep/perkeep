package escape

import "testing"

func TestQueryUnescapeAppend(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    string
		wantErr string
	}{
		{name: "zero", in: "", want: ""},
		{name: "no-escape", in: "hello world", want: "hello world"},
		{name: "escapes", in: "foo+bar%20baz", want: "foo bar baz"},
		{name: "truncated-escape", in: "foo%", wantErr: "invalid URL escape \"%\""},
		{name: "bad-hex", in: "foo%zy", wantErr: "invalid URL escape \"%zy\""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			base := []byte("prefix-")
			gotBytes, err := QueryUnescapeAppend(base, []byte(tt.in))
			if err != nil {
				if tt.wantErr == "" {
					t.Fatalf("QueryUnescapeAppend error: %v", err)
				}
				if err.Error() != tt.wantErr {
					t.Fatalf("QueryUnescapeAppend error = %v; want error %v", err, tt.wantErr)
				}
				return
			}
			if tt.wantErr != "" {
				t.Fatalf("QueryUnescapeAppend = %q, want error %v", gotBytes, tt.wantErr)
			}
			wantBytes := append([]byte("prefix-"), []byte(tt.want)...)
			if string(gotBytes) != string(wantBytes) {
				t.Errorf("QueryUnescapeAppend = %q, want %q", gotBytes, wantBytes)
			}
		})
	}
}

func TestAllocs(t *testing.T) {
	buf := make([]byte, 0, 100)
	in := []byte("foo+bar%20baz")
	n := testing.AllocsPerRun(1000, func() {
		got, err := QueryUnescapeAppend(buf[:0], in)
		if err != nil || string(got) != "foo bar baz" {
			t.Fatalf("QueryUnescapeAppend = %q, %v; want %q, nil", got, err, "foo bar baz")
		}
	})
	if n != 0 {
		t.Errorf("QueryUnescapeAppend allocs = %v; want 0", n)
	}
}
