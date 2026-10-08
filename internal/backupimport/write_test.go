package backupimport

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"fmt"
	"io"
	"reflect"
	"testing"
	"time"
)

type protoValue struct {
	wire uint64
	u    uint64
	data []byte
}

// Read the wire format independently of both the exporter and the importer.
func readProto(t *testing.T, data []byte) map[int][]protoValue {
	t.Helper()
	out := map[int][]protoValue{}
	readVarint := func() uint64 {
		u, n := binary.Uvarint(data)
		if n <= 0 {
			t.Fatal("invalid protobuf varint")
		}
		data = data[n:]
		return u
	}
	take := func(n uint64) []byte {
		if n > uint64(len(data)) {
			t.Fatal("truncated protobuf field")
		}
		b := data[:n]
		data = data[n:]
		return b
	}
	for len(data) > 0 {
		key := readVarint()
		num := int(key >> 3)
		if num == 0 {
			t.Fatal("invalid protobuf field number")
		}
		v := protoValue{wire: key & 7}
		switch v.wire {
		case 0:
			v.u = readVarint()
		case 1:
			v.u = binary.LittleEndian.Uint64(take(8))
		case 2:
			v.data = take(readVarint())
		case 5:
			v.u = uint64(binary.LittleEndian.Uint32(take(4)))
		default:
			t.Fatalf("unsupported protobuf wire type %d", v.wire)
		}
		out[num] = append(out[num], v)
	}
	return out
}

func gunzipMihon(t *testing.T, data []byte) []byte {
	t.Helper()
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	raw, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func mihonExportFixture() (*Backup, []MihonSourcePreferences) {
	epoch := time.UnixMilli(0).UTC()
	readAt := time.UnixMilli(1700000000000).UTC()
	return &Backup{
		Format: FormatMihon, Sources: map[string]string{"0": "", "42": "Example Source"},
		Categories: []string{"", "Reading"},
		Entries: []BackupManga{
			{
				SourceID: "0", URL: "/series/first", Status: "unknown", Favorite: true,
				Categories: []string{"", "Reading"},
				Trackers: map[string]string{
					TrackerAniList: "101", TrackerMAL: "102", TrackerKitsu: "103", TrackerMangaUpdates: "104",
				},
				Chapters: []BackupChapter{
					{URL: "", Name: "", ReadAt: &epoch},
					{URL: "/chapter/first", Name: "Chapter 1", Number: 1.5, Read: true, LastPageRead: 3, ReadAt: &readAt},
				},
			},
			{
				SourceID: "42", SourceName: "Example Source", URL: "/series/second", Status: "unknown",
				Trackers: map[string]string{TrackerMangaUpdates: "example-slug"},
			},
		},
	}, []MihonSourcePreferences{
		{SourceID: "0", Strings: map[string]string{"": "", "Address": "https://library.example.test"}},
		{SourceID: "42", Strings: map[string]string{}},
	}
}

func TestMarshalMihonRequiredFields(t *testing.T) {
	in, preferences := mihonExportFixture()
	in.Entries[0].URL = "" // Required even though mangarr rejects an empty manga URL on import.
	raw := gunzipMihon(t, MarshalMihon(in, preferences...))
	type message struct {
		required map[int]uint64 // field number to wire type
		children map[int]string
	}
	models := map[string]message{
		"Backup":                  {map[int]uint64{1: 2}, map[int]string{1: "BackupManga", 2: "BackupCategory", 101: "BackupSource", 105: "BackupSourcePreferences"}},
		"BackupManga":             {map[int]uint64{1: 0, 2: 2}, map[int]string{16: "BackupChapter", 18: "BackupTracking", 104: "BackupHistory"}},
		"BackupChapter":           {map[int]uint64{1: 2, 2: 2}, nil},
		"BackupCategory":          {map[int]uint64{1: 2}, nil},
		"BackupHistory":           {map[int]uint64{1: 2, 2: 0}, nil},
		"BackupSource":            {map[int]uint64{2: 0}, nil},
		"BackupTracking":          {map[int]uint64{1: 0, 2: 0}, nil},
		"BackupSourcePreferences": {map[int]uint64{1: 2}, map[int]string{2: "BackupPreference"}},
		"BackupPreference":        {map[int]uint64{1: 2, 2: 2}, map[int]string{2: "PreferenceValue"}},
		"PreferenceValue":         {map[int]uint64{1: 2, 2: 2}, map[int]string{2: "StringPreferenceValue"}},
		"StringPreferenceValue":   {map[int]uint64{1: 2}, nil},
	}
	seen := map[string]int{}
	var walk func(*testing.T, string, []byte)
	walk = func(t *testing.T, name string, raw []byte) {
		t.Helper()
		seen[name]++
		fields := readProto(t, raw)
		for num, wire := range models[name].required {
			if len(fields[num]) == 0 {
				t.Errorf("%s: missing required field %d", name, num)
			}
			for _, v := range fields[num] {
				if v.wire != wire {
					t.Errorf("%s field %d: wire type %d, want %d", name, num, v.wire, wire)
				}
			}
		}
		if name == "BackupTracking" && len(fields[2]) == 1 && fields[2][0].u != 0 {
			t.Error("libraryId must be zero for an exported tracker")
		}
		if name == "PreferenceValue" && len(fields[1]) == 1 && string(fields[1][0].data) != "eu.kanade.tachiyomi.data.backup.models.StringPreferenceValue" {
			t.Error("incorrect preference class name")
		}
		if name == "BackupSourcePreferences" {
			// Required lists are absent only when empty. The first source has
			// two preferences; the second has none, without a dummy message.
			want := 0
			if seen[name] == 1 {
				want = 2
			}
			if len(fields[2]) != want {
				t.Errorf("prefs count = %d, want %d", len(fields[2]), want)
			}
		}
		for num, child := range models[name].children {
			for i, v := range fields[num] {
				t.Run(fmt.Sprintf("%s/%d", child, i), func(t *testing.T) {
					if v.wire != 2 {
						t.Fatalf("message wire type = %d, want 2", v.wire)
					}
					walk(t, child, v.data)
				})
			}
		}
	}
	walk(t, "Backup", raw)
	want := map[string]int{
		"Backup": 1, "BackupManga": 2, "BackupChapter": 2, "BackupCategory": 2,
		"BackupHistory": 2, "BackupSource": 2, "BackupTracking": 5,
		"BackupSourcePreferences": 2, "BackupPreference": 2, "PreferenceValue": 2, "StringPreferenceValue": 2,
	}
	if !reflect.DeepEqual(seen, want) {
		t.Fatalf("message counts = %v, want %v", seen, want)
	}
}

func TestMarshalMihonRequiredFieldsRoundTrip(t *testing.T) {
	in, preferences := mihonExportFixture()
	out, err := Parse(MarshalMihon(in, preferences...))
	if err != nil {
		t.Fatal(err)
	}
	// The importer treats nonpositive history timestamps as unknown. Source
	// preferences are Mihon-only and intentionally ignored by the importer.
	in.Entries[0].Chapters[0].ReadAt = nil
	if !reflect.DeepEqual(out, in) {
		t.Fatalf("round trip = %+v, want %+v", out, in)
	}
}

func TestMarshalMihonEmptyLibrary(t *testing.T) {
	for _, withPreferences := range []bool{false, true} {
		t.Run(fmt.Sprint(withPreferences), func(t *testing.T) {
			var preferences []MihonSourcePreferences
			if withPreferences {
				preferences = []MihonSourcePreferences{{SourceID: "0"}}
			}
			fields := readProto(t, gunzipMihon(t, MarshalMihon(&Backup{}, preferences...)))
			// ProtobufDecoder.readIfAbsent supplies empty required lists.
			// Field 1 with a zero-length payload would be an invalid manga.
			if len(fields[1]) != 0 {
				t.Fatal("empty library contains a manga message")
			}
			if withPreferences {
				if len(fields[105]) != 1 {
					t.Fatal("missing source preferences")
				}
				prefs := readProto(t, fields[105][0].data)
				if len(prefs[1]) != 1 || string(prefs[1][0].data) != "source_0" || len(prefs[2]) != 0 {
					t.Fatalf("empty source preferences = %v", prefs)
				}
			}
		})
	}
}
