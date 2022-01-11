package xtreamcodes

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Timestamp is a helper struct to convert unix timestamp ints and strings to time.Time.
type Timestamp struct {
	time.Time
	quoted bool
}

// MarshalJSON returns the Unix timestamp as a string.
func (t Timestamp) MarshalJSON() ([]byte, error) {
	if t.quoted {
		return []byte(`"` + strconv.FormatInt(t.Time.Unix(), 10) + `"`), nil
	}
	return []byte(strconv.FormatInt(t.Time.Unix(), 10)), nil
}

// UnmarshalJSON converts the int or string to a Unix timestamp.
func (t *Timestamp) UnmarshalJSON(b []byte) error {
	// Timestamps are sometimes quoted, sometimes not, lets just always remove quotes just in case...
	t.quoted = strings.Contains(string(b), `"`)
	ts, err := strconv.Atoi(strings.Replace(string(b), `"`, "", -1))
	if err != nil {
		return err
	}
	t.Time = time.Unix(int64(ts), 0)
	return nil
}

// ConvertibleBoolean is a helper type to allow JSON documents using 0/1 or "true" and "false" be converted to bool.
type ConvertibleBoolean struct {
	bool
	quoted bool
}

// MarshalJSON returns a 0 or 1 depending on bool state.
func (bit ConvertibleBoolean) MarshalJSON() ([]byte, error) {
	var bitSetVar int8
	if bit.bool {
		bitSetVar = 1
	}

	if bit.quoted {
		return json.Marshal(fmt.Sprint(bitSetVar))
	}

	return json.Marshal(bitSetVar)
}

// UnmarshalJSON converts a 0, 1, true or false into a bool
func (bit *ConvertibleBoolean) UnmarshalJSON(data []byte) error {
	bit.quoted = strings.Contains(string(data), `"`)
	// Bools as ints are sometimes quoted, sometimes not, lets just always remove quotes just in case...
	asString := strings.Replace(string(data), `"`, "", -1)
	if asString == "1" || asString == "true" {
		bit.bool = true
	} else if asString == "0" || asString == "false" {
		bit.bool = false
	} else {
		return fmt.Errorf("Boolean unmarshal error: invalid input %s", asString)
	}
	return nil
}

// FlexInt is a int64 which unmarshals from JSON
// as either unquoted or quoted (with any amount
// of internal leading/trailing whitespace).
// Originally found at https://bit.ly/2NkJ0SK and
// https://play.golang.org/p/KNPxDL1yqL
type FlexInt int64

func (f FlexInt) MarshalJSON() ([]byte, error) {
	return json.Marshal(int64(f))
}

func (f *FlexInt) UnmarshalJSON(data []byte) error {
	var v int64

	data = bytes.Trim(data, `" `)

	err := json.Unmarshal(data, &v)
	*f = FlexInt(v)
	return err
}

type FlexFloat float64

func (ff *FlexFloat) UnmarshalJSON(b []byte) error {
	if b[0] != '"' {
		return json.Unmarshal(b, (*float64)(ff))
	}

	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}

	if len(s) == 0 {
		s = "0"
	}

	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		f = 0
	}
	*ff = FlexFloat(f)
	return nil
}

// JSONStringSlice is a struct containing a slice of strings.
// It is needed for cases in which we may get an array or may get
// a single string in a JSON response.
type JSONStringSlice struct {
	Slice        []string `json:"-"`
	SingleString bool     `json:"-"`
}

// MarshalJSON returns b as the JSON encoding of b.
func (b JSONStringSlice) MarshalJSON() ([]byte, error) {
	if !b.SingleString {
		return json.Marshal(b.Slice)
	}
	return json.Marshal(b.Slice[0])
}

// UnmarshalJSON sets *b to a copy of data.
func (b *JSONStringSlice) UnmarshalJSON(data []byte) error {
	if data[0] == '"' {
		data = append([]byte(`[`), data...)
		data = append(data, []byte(`]`)...)
		b.SingleString = true
	}

	return json.Unmarshal(data, &b.Slice)
}
