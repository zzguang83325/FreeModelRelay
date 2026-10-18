package kit

import "testing"

// 基准向量由官方 id.ts(v1.19.0) bun 实测生成:
//
//	create("ses","descending",T) 新进程首调 counter=1, 次调 counter=2。
//
// 用途: 守护手移植的官方 ID 算法(免费通道指纹要素之一)。
func TestZenOfficialIDAtOfficialVectors(t *testing.T) {
	cases := []struct {
		ms      int64
		counter uint64
		prefix  string
		wantHex string
	}{
		{1760000000000, 1, "ses", "637d33fffffe"},
		{1792000000000, 1, "ses", "ec479ffffffe"},
		{1792771200000, 1, "ses", "e96826bffffe"},
		{1792771200000, 2, "msg", "e96826bffffd"},
	}
	for _, c := range cases {
		got := zenOfficialIDAt(c.prefix, c.ms, c.counter)
		if len(got) != 30 || got[:4] != c.prefix+"_" {
			t.Fatalf("%v: bad shape %q", c, got)
		}
		if got[4:16] != c.wantHex {
			t.Errorf("ms=%d counter=%d: hex = %q, want %q", c.ms, c.counter, got[4:16], c.wantHex)
		}
		for _, r := range got[16:] {
			if !byteIn(byte(r), zenBase62) {
				t.Errorf("ms=%d: random seg %q has non-base62 %q", c.ms, got[16:], r)
			}
		}
	}
}

func byteIn(b byte, set string) bool {
	for i := 0; i < len(set); i++ {
		if set[i] == b {
			return true
		}
	}
	return false
}
