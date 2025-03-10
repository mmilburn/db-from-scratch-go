package byodb12

import (
	is "github.com/stretchr/testify/require"
	"math"
	"os"
	"reflect"
	"sort"
	"testing"
)

type R struct {
	db  DB
	ref map[string][]Record
}

func newR() *R {
	os.Remove("r.db")
	r := &R{
		db:  DB{Path: "r.db"},
		ref: map[string][]Record{},
	}
	err := r.db.Open()
	assert(err == nil)
	return r
}

func (r *R) dispose() {
	r.db.Close()
	os.Remove("r.db")
}

func (r *R) begin() *DBTX {
	tx := DBTX{}
	r.db.Begin(&tx)
	return &tx
}

func (r *R) commit(tx *DBTX) {
	err := r.db.Commit(tx)
	assert(err == nil)
}

func (r *R) create(tdef *TableDef) {
	tx := r.begin()
	err := tx.TableNew(tdef)
	r.commit(tx)
	assert(err == nil)
}

func (r *R) findRef(table string, rec Record) int {
	pkeys := len(r.db.tables[table].Indexes[0])
	records := r.ref[table]
	found := -1
	for i, old := range records {
		if reflect.DeepEqual(old.Vals[:pkeys], rec.Vals[:pkeys]) {
			assert(found == -1)
			found = i
		}
	}
	return found
}

func (r *R) add(table string, rec Record) bool {
	tx := r.begin()
	dbreq := DBUpdateReq{Record: rec}
	_, err := tx.Set(table, &dbreq)
	assert(err == nil)
	r.commit(tx)

	records := r.ref[table]
	idx := r.findRef(table, rec)
	assert((idx < 0) == dbreq.Added)
	if idx < 0 {
		r.ref[table] = append(records, rec)
	} else {
		records[idx] = rec
	}
	return dbreq.Added
}

func (r *R) del(table string, rec Record) bool {
	tx := r.begin()
	deleted, err := tx.Delete(table, rec)
	assert(err == nil)
	r.commit(tx)

	idx := r.findRef(table, rec)
	if deleted {
		assert(idx >= 0)
		records := r.ref[table]
		copy(records[idx:], records[idx+1:])
		r.ref[table] = records[:len(records)-1]
	} else {
		assert(idx == -1)
	}

	return deleted
}

func (r *R) get(table string, rec *Record) bool {
	tx := r.begin()
	ok, err := tx.Get(table, rec)
	assert(err == nil)
	r.commit(tx)
	idx := r.findRef(table, *rec)
	if ok {
		assert(idx >= 0)
		records := r.ref[table]
		assert(reflect.DeepEqual(records[idx], *rec))
	} else {
		assert(idx < 0)
	}
	return ok
}

func TestTableCreate(t *testing.T) {
	r := newR()
	tdef := &TableDef{
		Name:    "tbl_test",
		Cols:    []string{"ki1", "ks2", "s1", "i2"},
		Types:   []uint32{TYPE_INT64, TYPE_BYTES, TYPE_BYTES, TYPE_INT64},
		Indexes: [][]string{{"ki1", "ks2"}},
	}
	r.create(tdef)

	tdef = &TableDef{
		Name:    "tbl_test2",
		Cols:    []string{"ki1", "ks2"},
		Types:   []uint32{TYPE_INT64, TYPE_BYTES},
		Indexes: [][]string{{"ki1", "ks2"}},
	}
	r.create(tdef)

	tx := r.begin()
	{
		rec := (&Record{}).AddStr("key", []byte("next_prefix"))
		ok, err := tx.Get("@meta", rec)
		assert(ok && err == nil)
		is.Equal(t, []byte{102, 0, 0, 0}, rec.Get("val").Str)
	}
	{
		rec := (&Record{}).AddStr("name", []byte("tbl_test"))
		ok, err := tx.Get("@table", rec)
		assert(ok && err == nil)
		expected := `{"Name":"tbl_test","Types":[2,1,1,2],"Cols":["ki1","ks2","s1","i2"],"Indexes":[["ki1","ks2"]],"Prefixes":[100]}`
		is.Equal(t, expected, string(rec.Get("def").Str))
	}
	r.commit(tx)

	r.dispose()
}

func TestTableBasic(t *testing.T) {
	r := newR()
	tdef := &TableDef{
		Name:    "tbl_test",
		Cols:    []string{"ki1", "ks2", "s1", "i2"},
		Types:   []uint32{TYPE_INT64, TYPE_BYTES, TYPE_BYTES, TYPE_INT64},
		Indexes: [][]string{{"ki1", "ks2"}},
	}
	r.create(tdef)

	rec := Record{}
	rec.AddInt64("ki1", 1).AddStr("ks2", []byte("hello"))
	rec.AddStr("s1", []byte("world")).AddInt64("i2", 2)
	added := r.add("tbl_test", rec)
	is.True(t, added)

	{
		got := Record{}
		got.AddInt64("ki1", 1).AddStr("ks2", []byte("hello"))
		ok := r.get("tbl_test", &got)
		is.True(t, ok)
	}
	{
		got := Record{}
		got.AddInt64("ki1", 1).AddStr("ks2", []byte("hello2"))
		ok := r.get("tbl_test", &got)
		is.False(t, ok)
	}

	rec.Get("s1").Str = []byte("www")
	added = r.add("tbl_test", rec)
	is.False(t, added)

	{
		got := Record{}
		got.AddInt64("ki1", 1).AddStr("ks2", []byte("hello"))
		ok := r.get("tbl_test", &got)
		is.True(t, ok)
	}

	{
		key := Record{}
		key.AddInt64("ki1", 1).AddStr("ks2", []byte("hello2"))
		deleted := r.del("tbl_test", key)
		is.False(t, deleted)

		key.Get("ks2").Str = []byte("hello")
		deleted = r.del("tbl_test", key)
		is.True(t, deleted)
	}

	r.dispose()
}

func TestStringEscape(t *testing.T) {
	in := [][]byte{
		{},
		{0},
		{1},
	}
	out := [][]byte{
		{},
		{1, 1},
		{1, 2},
	}
	for i, s := range in {
		b := escapeString(s)
		is.Equal(t, out[i], b)
		s2 := unescapeString(b)
		is.Equal(t, s, s2)
	}
}

func TestTableEncoding(t *testing.T) {
	input := []int{-1, 0, +1, math.MinInt64, math.MaxInt64}
	sort.Ints(input)

	encoded := []string{}
	for _, i := range input {
		v := Value{Type: TYPE_INT64, I64: int64(i)}
		b := encodeValues(nil, []Value{v})
		out := []Value{v}
		decodeValues(b, out)
		assert(out[0].I64 == int64(i))
		encoded = append(encoded, string(b))
	}

	is.True(t, sort.StringsAreSorted(encoded))
}

func TestTableScan(t *testing.T) {
	r := newR()
	tdef := &TableDef{
		Name:  "tbl_test",
		Cols:  []string{"ki1", "ks2", "s1", "i2"},
		Types: []uint32{TYPE_INT64, TYPE_BYTES, TYPE_BYTES, TYPE_INT64},
		Indexes: [][]string{
			{"ki1", "ks2"},
			{"i2"},
		},
	}
	r.create(tdef)

	size := 100
	for i := 0; i < size; i += 2 {
		rec := Record{}
		rec.AddInt64("ki1", int64(i)).AddStr("ks2", []byte("hello"))
		rec.AddStr("s1", []byte("world")).AddInt64("i2", int64(i/2))
		added := r.add("tbl_test", rec)
		assert(added)
	}

	// full table scan without a key
	tx := r.begin()
	{
		rec := Record{} // empty
		req := Scanner{
			Cmp1: CMP_GE, Cmp2: CMP_LE,
			Key1: rec, Key2: rec,
		}
		err := tx.Scan("tbl_test", &req)
		assert(err == nil)

		got := []Record{}
		for req.Valid() {
			rec := Record{}
			req.Deref(&rec)
			got = append(got, rec)
			req.Next()
		}
		is.Equal(t, r.ref["tbl_test"], got)
	}
	r.commit(tx)

	tmpkey := func(n int) Record {
		rec := Record{}
		rec.AddInt64("ki1", int64(n)) // partial primary key
		return rec
	}
	i2key := func(n int) Record {
		rec := Record{}
		rec.AddInt64("i2", int64(n)/2) // secondary index
		return rec
	}

	tx = r.begin()
	for i := 0; i < size; i += 2 {
		ref := []int64{}
		for j := i; j < size; j += 2 {
			ref = append(ref, int64(j))

			scanners := []Scanner{
				{
					Cmp1: CMP_GE,
					Cmp2: CMP_LE,
					Key1: tmpkey(i),
					Key2: tmpkey(j),
				},
				{
					Cmp1: CMP_GE,
					Cmp2: CMP_LE,
					Key1: tmpkey(i - 1),
					Key2: tmpkey(j + 1),
				},
				{
					Cmp1: CMP_GT,
					Cmp2: CMP_LT,
					Key1: tmpkey(i - 1),
					Key2: tmpkey(j + 1),
				},
				{
					Cmp1: CMP_GT,
					Cmp2: CMP_LT,
					Key1: tmpkey(i - 2),
					Key2: tmpkey(j + 2),
				},
				{
					Cmp1: CMP_GE,
					Cmp2: CMP_LE,
					Key1: i2key(i),
					Key2: i2key(j),
				},
				{
					Cmp1: CMP_GT,
					Cmp2: CMP_LT,
					Key1: i2key(i - 2),
					Key2: i2key(j + 2),
				},
			}
			for _, tmp := range scanners {
				tmp.Cmp1, tmp.Cmp2 = tmp.Cmp2, tmp.Cmp1
				tmp.Key1, tmp.Key2 = tmp.Key2, tmp.Key1
				scanners = append(scanners, tmp)
			}

			for _, sc := range scanners {
				err := tx.Scan("tbl_test", &sc)
				assert(err == nil)

				keys := []int64{}
				got := Record{}
				for sc.Valid() {
					sc.Deref(&got)
					keys = append(keys, got.Get("ki1").I64)
					sc.Next()
				}
				if sc.Cmp1 < sc.Cmp2 {
					// reverse
					for a := 0; a < len(keys)/2; a++ {
						b := len(keys) - 1 - a
						keys[a], keys[b] = keys[b], keys[a]
					}
				}

				is.Equal(t, ref, keys)
			} // scanners
		} // j
	} // i
	r.commit(tx)

	r.dispose()
}

func TestTableIndex(t *testing.T) {
	r := newR()
	tdef := &TableDef{
		Name:  "tbl_test",
		Cols:  []string{"ki1", "ks2", "s1", "i2"},
		Types: []uint32{TYPE_INT64, TYPE_BYTES, TYPE_BYTES, TYPE_INT64},
		Indexes: [][]string{
			{"ki1", "ks2"},
			{"ks2", "ki1"},
			{"i2"},
			{"ki1", "i2"},
		},
	}
	r.create(tdef)

	record := func(ki1 int64, ks2 string, s1 string, i2 int64) Record {
		rec := Record{}
		rec.AddInt64("ki1", ki1).AddStr("ks2", []byte(ks2))
		rec.AddStr("s1", []byte(s1)).AddInt64("i2", i2)
		return rec
	}

	r1 := record(1, "a1", "v1", 2)
	r2 := record(2, "a2", "v2", -2)
	r.add("tbl_test", r1)
	r.add("tbl_test", r2)

	tx := r.begin()
	{
		rec := Record{}
		rec.AddInt64("i2", 2)
		req := Scanner{
			Cmp1: CMP_GE, Cmp2: CMP_LE,
			Key1: rec, Key2: rec,
		}
		err := tx.Scan("tbl_test", &req)
		assert(err == nil)
		is.True(t, req.Valid())

		out := Record{}
		req.Deref(&out)
		is.Equal(t, r1, out)

		req.Next()
		is.False(t, req.Valid())
	}
	r.commit(tx)

	tx = r.begin()
	{
		rec1 := Record{}
		rec1.AddInt64("i2", 2)
		rec2 := Record{}
		rec2.AddInt64("i2", 4)
		req := Scanner{
			Cmp1: CMP_GT, Cmp2: CMP_LE,
			Key1: rec1, Key2: rec2,
		}
		err := tx.Scan("tbl_test", &req)
		assert(err == nil)
		is.False(t, req.Valid())
	}
	r.commit(tx)

	r.add("tbl_test", record(1, "a1", "v1", 1))
	tx = r.begin()
	{
		rec := Record{}
		rec.AddInt64("i2", 2)
		req := Scanner{
			Cmp1: CMP_GE, Cmp2: CMP_LE,
			Key1: rec, Key2: rec,
		}
		err := tx.Scan("tbl_test", &req)
		assert(err == nil)
		is.False(t, req.Valid())
	}
	r.commit(tx)

	tx = r.begin()
	{
		rec := Record{}
		rec.AddInt64("i2", 1)
		req := Scanner{
			Cmp1: CMP_GE, Cmp2: CMP_LE,
			Key1: rec, Key2: rec,
		}
		err := tx.Scan("tbl_test", &req)
		assert(err == nil)
		is.True(t, req.Valid())
	}
	r.commit(tx)

	{
		rec := Record{}
		rec.AddInt64("ki1", 1).AddStr("ks2", []byte("a1"))
		ok := r.del("tbl_test", rec)
		assert(ok)
	}

	tx = r.begin()
	{
		rec := Record{}
		rec.AddInt64("i2", 1)
		req := Scanner{
			Cmp1: CMP_GE, Cmp2: CMP_LE,
			Key1: rec, Key2: rec,
		}
		err := tx.Scan("tbl_test", &req)
		assert(err == nil)
		is.False(t, req.Valid())
	}
	r.commit(tx)

	r.dispose()
}
