package builtins

import (
	"encoding/binary"
	"math"
	"strings"

	"undalang/object"
)

// memType is one of the C types mem can read and write
type memType struct {
	name   string
	size   int
	float  bool
	signed bool
}

var memTypes = []memType{
	{"i8", 1, false, true}, {"i16", 2, false, true}, {"i32", 4, false, true}, {"i64", 8, false, true},
	{"u8", 1, false, false}, {"u16", 2, false, false}, {"u32", 4, false, false}, {"u64", 8, false, false},
	{"f32", 4, true, false}, {"f64", 8, true, false},
}

// memModule is the mem standard module. the native runtime's um_* functions
// make the same checks with the same messages, keep them in step
func memModule() map[string]object.Object {
	members := map[string]object.Object{
		"alloc":       builtin(memAlloc),
		"free":        builtin(memFree),
		"size":        builtin(memSize),
		"get_cstring": builtin(memGetCString),
		"set_cstring": builtin(memSetCString),
	}
	for _, t := range memTypes {
		members["get_"+t.name] = builtin(memGet(t))
		members["set_"+t.name] = builtin(memSet(t))
	}
	return members
}

func argCount(name string, args []object.Object, n int) object.Object {
	if len(args) == n {
		return nil
	}
	return errorf("%s expects %d %s, got %d", name, n, map[bool]string{true: "argument", false: "arguments"}[n == 1], len(args))
}

func memAlloc(args ...object.Object) object.Object {
	if err := argCount("mem.alloc", args, 1); err != nil {
		return err
	}
	n, ok := args[0].(*object.Integer)
	if !ok {
		return errorf("mem.alloc size must be INTEGER, got %s", args[0].Type())
	}
	if n.Value < 0 {
		return errorf("mem.alloc size can't be negative, got %d", n.Value)
	}
	return &object.Buffer{Data: make([]byte, n.Value)}
}

// liveBuffer checks a buffer argument
func liveBuffer(name string, v object.Object) (*object.Buffer, object.Object) {
	b, ok := v.(*object.Buffer)
	if !ok {
		return nil, errorf("%s first argument must be BUFFER, got %s", name, v.Type())
	}
	if b.Freed {
		return nil, errorf("%s on a freed buffer", name)
	}
	return b, nil
}

func memFree(args ...object.Object) object.Object {
	if err := argCount("mem.free", args, 1); err != nil {
		return err
	}
	b, err := liveBuffer("mem.free", args[0])
	if err != nil {
		return err
	}
	b.Freed, b.Data = true, nil
	return &object.Nil{}
}

func memSize(args ...object.Object) object.Object {
	if err := argCount("mem.size", args, 1); err != nil {
		return err
	}
	b, err := liveBuffer("mem.size", args[0])
	if err != nil {
		return err
	}
	return object.NewInt(int64(len(b.Data)))
}

// bufOffset checks the argument count, the buffer and the offset, in that
// order. inRange then checks width bytes at offset fit
func bufOffset(name string, args []object.Object, n int) (*object.Buffer, int64, object.Object) {
	if err := argCount(name, args, n); err != nil {
		return nil, 0, err
	}
	b, err := liveBuffer(name, args[0])
	if err != nil {
		return nil, 0, err
	}
	off, ok := args[1].(*object.Integer)
	if !ok {
		return nil, 0, errorf("%s offset must be INTEGER, got %s", name, args[1].Type())
	}
	return b, off.Value, nil
}

func inRange(name string, b *object.Buffer, off int64, width int) object.Object {
	if off < 0 || off > int64(len(b.Data))-int64(width) {
		return errorf("%s offset %d is out of range for a %d byte buffer", name, off, len(b.Data))
	}
	return nil
}

// span is bufOffset and inRange together, returning the bytes
func span(name string, args []object.Object, n, width int) ([]byte, object.Object) {
	b, off, err := bufOffset(name, args, n)
	if err != nil {
		return nil, err
	}
	if err := inRange(name, b, off, width); err != nil {
		return nil, err
	}
	return b.Data[off : off+int64(width)], nil
}

func memGet(t memType) object.BuiltinFunction {
	name := "mem.get_" + t.name
	return func(args ...object.Object) object.Object {
		p, err := span(name, args, 2, t.size)
		if err != nil {
			return err
		}
		var u uint64
		switch t.size {
		case 1:
			u = uint64(p[0])
		case 2:
			u = uint64(binary.NativeEndian.Uint16(p))
		case 4:
			u = uint64(binary.NativeEndian.Uint32(p))
		case 8:
			u = binary.NativeEndian.Uint64(p)
		}
		switch {
		case t.float && t.size == 4:
			return &object.Float{Value: float64(math.Float32frombits(uint32(u)))}
		case t.float:
			return &object.Float{Value: math.Float64frombits(u)}
		case t.signed:
			shift := 64 - 8*t.size
			return object.NewInt(int64(u<<shift) >> shift)
		case u > math.MaxInt64:
			return errorf("%s read %d, too big for an int", name, u)
		}
		return object.NewInt(int64(u))
	}
}

func memSet(t memType) object.BuiltinFunction {
	name := "mem.set_" + t.name
	return func(args ...object.Object) object.Object {
		p, err := span(name, args, 3, t.size)
		if err != nil {
			return err
		}
		var u uint64
		if t.float {
			f, ok := number(args[2])
			if !ok {
				return errorf("%s value must be number, got %s", name, args[2].Type())
			}
			if t.size == 4 {
				u = uint64(math.Float32bits(float32(f)))
			} else {
				u = math.Float64bits(f)
			}
		} else {
			v, ok := args[2].(*object.Integer)
			if !ok {
				return errorf("%s value must be INTEGER, got %s", name, args[2].Type())
			}
			bits := uint(8 * t.size)
			fits := v.Value >= 0 && (bits == 64 || v.Value < int64(1)<<bits)
			if t.signed {
				fits = bits == 64 || (v.Value >= -(int64(1)<<(bits-1)) && v.Value < int64(1)<<(bits-1))
			}
			if !fits {
				return errorf("%s value is out of range for %s: %d", name, t.name, v.Value)
			}
			u = uint64(v.Value)
		}
		switch t.size {
		case 1:
			p[0] = byte(u)
		case 2:
			binary.NativeEndian.PutUint16(p, uint16(u))
		case 4:
			binary.NativeEndian.PutUint32(p, uint32(u))
		case 8:
			binary.NativeEndian.PutUint64(p, u)
		}
		return &object.Nil{}
	}
}

func memGetCString(args ...object.Object) object.Object {
	b, off, err := bufOffset("mem.get_cstring", args, 2)
	if err != nil {
		return err
	}
	if err := inRange("mem.get_cstring", b, off, 0); err != nil {
		return err
	}
	for i := off; i < int64(len(b.Data)); i++ {
		if b.Data[i] == 0 {
			return &object.String{Value: string(b.Data[off:i])}
		}
	}
	return errorf("mem.get_cstring found no NUL byte after offset %d", off)
}

func memSetCString(args ...object.Object) object.Object {
	b, off, err := bufOffset("mem.set_cstring", args, 3)
	if err != nil {
		return err
	}
	s, ok := args[2].(*object.String)
	if !ok {
		return errorf("mem.set_cstring value must be STRING, got %s", args[2].Type())
	}
	if strings.IndexByte(s.Value, 0) >= 0 {
		return errorf("mem.set_cstring value contains a NUL byte")
	}
	if err := inRange("mem.set_cstring", b, off, len(s.Value)+1); err != nil {
		return err
	}
	copy(b.Data[off:], s.Value)
	b.Data[off+int64(len(s.Value))] = 0
	return &object.Nil{}
}
