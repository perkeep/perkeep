"use strict";
(function() {

Error.stackTraceLimit = Infinity;

var $global, $module;
if (typeof window !== "undefined") { /* web page */
  $global = window;
} else if (typeof self !== "undefined") { /* web worker */
  $global = self;
} else if (typeof global !== "undefined") { /* Node.js */
  $global = global;
  $global.require = require;
} else { /* others (e.g. Nashorn) */
  $global = this;
}

if ($global === undefined || $global.Array === undefined) {
  throw new Error("no global object found");
}
if (typeof module !== "undefined") {
  $module = module;
}

var $packages = {}, $idCounter = 0;
var $keys = function(m) { return m ? Object.keys(m) : []; };
var $min = Math.min;
var $mod = function(x, y) { return x % y; };
var $parseInt = parseInt;
var $parseFloat = function(f) {
  if (f !== undefined && f !== null && f.constructor === Number) {
    return f;
  }
  return parseFloat(f);
};
var $flushConsole = function() {};
var $throwRuntimeError; /* set by package "runtime" */
var $throwNilPointerError = function() { $throwRuntimeError("invalid memory address or nil pointer dereference"); };
var $call = function(fn, rcvr, args) { return fn.apply(rcvr, args); };

var $froundBuf = new Float32Array(1);
var $fround = Math.fround || function(f) { $froundBuf[0] = f; return $froundBuf[0]; };

var $mapArray = function(array, f) {
  var newArray = new array.constructor(array.length);
  for (var i = 0; i < array.length; i++) {
    newArray[i] = f(array[i]);
  }
  return newArray;
};

var $methodVal = function(recv, name) {
  var vals = recv.$methodVals || {};
  recv.$methodVals = vals; /* noop for primitives */
  var f = vals[name];
  if (f !== undefined) {
    return f;
  }
  var method = recv[name];
  f = function() {
    $stackDepthOffset--;
    try {
      return method.apply(recv, arguments);
    } finally {
      $stackDepthOffset++;
    }
  };
  vals[name] = f;
  return f;
};

var $methodExpr = function(method) {
  if (method.$expr === undefined) {
    method.$expr = function() {
      $stackDepthOffset--;
      try {
        return Function.call.apply(method, arguments);
      } finally {
        $stackDepthOffset++;
      }
    };
  }
  return method.$expr;
};

var $subslice = function(slice, low, high, max) {
  if (low < 0 || high < low || max < high || high > slice.$capacity || max > slice.$capacity) {
    $throwRuntimeError("slice bounds out of range");
  }
  var s = new slice.constructor(slice.$array);
  s.$offset = slice.$offset + low;
  s.$length = slice.$length - low;
  s.$capacity = slice.$capacity - low;
  if (high !== undefined) {
    s.$length = high - low;
  }
  if (max !== undefined) {
    s.$capacity = max - low;
  }
  return s;
};

var $sliceToArray = function(slice) {
  if (slice.$length === 0) {
    return [];
  }
  if (slice.$array.constructor !== Array) {
    return slice.$array.subarray(slice.$offset, slice.$offset + slice.$length);
  }
  return slice.$array.slice(slice.$offset, slice.$offset + slice.$length);
};

var $decodeRune = function(str, pos) {
  var c0 = str.charCodeAt(pos);

  if (c0 < 0x80) {
    return [c0, 1];
  }

  if (c0 !== c0 || c0 < 0xC0) {
    return [0xFFFD, 1];
  }

  var c1 = str.charCodeAt(pos + 1);
  if (c1 !== c1 || c1 < 0x80 || 0xC0 <= c1) {
    return [0xFFFD, 1];
  }

  if (c0 < 0xE0) {
    var r = (c0 & 0x1F) << 6 | (c1 & 0x3F);
    if (r <= 0x7F) {
      return [0xFFFD, 1];
    }
    return [r, 2];
  }

  var c2 = str.charCodeAt(pos + 2);
  if (c2 !== c2 || c2 < 0x80 || 0xC0 <= c2) {
    return [0xFFFD, 1];
  }

  if (c0 < 0xF0) {
    var r = (c0 & 0x0F) << 12 | (c1 & 0x3F) << 6 | (c2 & 0x3F);
    if (r <= 0x7FF) {
      return [0xFFFD, 1];
    }
    if (0xD800 <= r && r <= 0xDFFF) {
      return [0xFFFD, 1];
    }
    return [r, 3];
  }

  var c3 = str.charCodeAt(pos + 3);
  if (c3 !== c3 || c3 < 0x80 || 0xC0 <= c3) {
    return [0xFFFD, 1];
  }

  if (c0 < 0xF8) {
    var r = (c0 & 0x07) << 18 | (c1 & 0x3F) << 12 | (c2 & 0x3F) << 6 | (c3 & 0x3F);
    if (r <= 0xFFFF || 0x10FFFF < r) {
      return [0xFFFD, 1];
    }
    return [r, 4];
  }

  return [0xFFFD, 1];
};

var $encodeRune = function(r) {
  if (r < 0 || r > 0x10FFFF || (0xD800 <= r && r <= 0xDFFF)) {
    r = 0xFFFD;
  }
  if (r <= 0x7F) {
    return String.fromCharCode(r);
  }
  if (r <= 0x7FF) {
    return String.fromCharCode(0xC0 | r >> 6, 0x80 | (r & 0x3F));
  }
  if (r <= 0xFFFF) {
    return String.fromCharCode(0xE0 | r >> 12, 0x80 | (r >> 6 & 0x3F), 0x80 | (r & 0x3F));
  }
  return String.fromCharCode(0xF0 | r >> 18, 0x80 | (r >> 12 & 0x3F), 0x80 | (r >> 6 & 0x3F), 0x80 | (r & 0x3F));
};

var $stringToBytes = function(str) {
  var array = new Uint8Array(str.length);
  for (var i = 0; i < str.length; i++) {
    array[i] = str.charCodeAt(i);
  }
  return array;
};

var $bytesToString = function(slice) {
  if (slice.$length === 0) {
    return "";
  }
  var str = "";
  for (var i = 0; i < slice.$length; i += 10000) {
    str += String.fromCharCode.apply(null, slice.$array.subarray(slice.$offset + i, slice.$offset + Math.min(slice.$length, i + 10000)));
  }
  return str;
};

var $stringToRunes = function(str) {
  var array = new Int32Array(str.length);
  var rune, j = 0;
  for (var i = 0; i < str.length; i += rune[1], j++) {
    rune = $decodeRune(str, i);
    array[j] = rune[0];
  }
  return array.subarray(0, j);
};

var $runesToString = function(slice) {
  if (slice.$length === 0) {
    return "";
  }
  var str = "";
  for (var i = 0; i < slice.$length; i++) {
    str += $encodeRune(slice.$array[slice.$offset + i]);
  }
  return str;
};

var $copyString = function(dst, src) {
  var n = Math.min(src.length, dst.$length);
  for (var i = 0; i < n; i++) {
    dst.$array[dst.$offset + i] = src.charCodeAt(i);
  }
  return n;
};

var $copySlice = function(dst, src) {
  var n = Math.min(src.$length, dst.$length);
  $internalCopy(dst.$array, src.$array, dst.$offset, src.$offset, n, dst.constructor.elem);
  return n;
};

var $copy = function(dst, src, typ) {
  switch (typ.kind) {
  case $kindArray:
    $internalCopy(dst, src, 0, 0, src.length, typ.elem);
    break;
  case $kindStruct:
    for (var i = 0; i < typ.fields.length; i++) {
      var f = typ.fields[i];
      switch (f.typ.kind) {
      case $kindArray:
      case $kindStruct:
        $copy(dst[f.prop], src[f.prop], f.typ);
        continue;
      default:
        dst[f.prop] = src[f.prop];
        continue;
      }
    }
    break;
  }
};

var $internalCopy = function(dst, src, dstOffset, srcOffset, n, elem) {
  if (n === 0 || (dst === src && dstOffset === srcOffset)) {
    return;
  }

  if (src.subarray) {
    dst.set(src.subarray(srcOffset, srcOffset + n), dstOffset);
    return;
  }

  switch (elem.kind) {
  case $kindArray:
  case $kindStruct:
    if (dst === src && dstOffset > srcOffset) {
      for (var i = n - 1; i >= 0; i--) {
        $copy(dst[dstOffset + i], src[srcOffset + i], elem);
      }
      return;
    }
    for (var i = 0; i < n; i++) {
      $copy(dst[dstOffset + i], src[srcOffset + i], elem);
    }
    return;
  }

  if (dst === src && dstOffset > srcOffset) {
    for (var i = n - 1; i >= 0; i--) {
      dst[dstOffset + i] = src[srcOffset + i];
    }
    return;
  }
  for (var i = 0; i < n; i++) {
    dst[dstOffset + i] = src[srcOffset + i];
  }
};

var $clone = function(src, type) {
  var clone = type.zero();
  $copy(clone, src, type);
  return clone;
};

var $pointerOfStructConversion = function(obj, type) {
  if(obj.$proxies === undefined) {
    obj.$proxies = {};
    obj.$proxies[obj.constructor.string] = obj;
  }
  var proxy = obj.$proxies[type.string];
  if (proxy === undefined) {
    var properties = {};
    for (var i = 0; i < type.elem.fields.length; i++) {
      (function(fieldProp) {
        properties[fieldProp] = {
          get: function() { return obj[fieldProp]; },
          set: function(value) { obj[fieldProp] = value; },
        };
      })(type.elem.fields[i].prop);
    }
    proxy = Object.create(type.prototype, properties);
    proxy.$val = proxy;
    obj.$proxies[type.string] = proxy;
    proxy.$proxies = obj.$proxies;
  }
  return proxy;
};

var $append = function(slice) {
  return $internalAppend(slice, arguments, 1, arguments.length - 1);
};

var $appendSlice = function(slice, toAppend) {
  return $internalAppend(slice, toAppend.$array, toAppend.$offset, toAppend.$length);
};

var $internalAppend = function(slice, array, offset, length) {
  if (length === 0) {
    return slice;
  }

  var newArray = slice.$array;
  var newOffset = slice.$offset;
  var newLength = slice.$length + length;
  var newCapacity = slice.$capacity;

  if (newLength > newCapacity) {
    newOffset = 0;
    newCapacity = Math.max(newLength, slice.$capacity < 1024 ? slice.$capacity * 2 : Math.floor(slice.$capacity * 5 / 4));

    if (slice.$array.constructor === Array) {
      newArray = slice.$array.slice(slice.$offset, slice.$offset + slice.$length);
      newArray.length = newCapacity;
      var zero = slice.constructor.elem.zero;
      for (var i = slice.$length; i < newCapacity; i++) {
        newArray[i] = zero();
      }
    } else {
      newArray = new slice.$array.constructor(newCapacity);
      newArray.set(slice.$array.subarray(slice.$offset, slice.$offset + slice.$length));
    }
  }

  $internalCopy(newArray, array, newOffset + slice.$length, offset, length, slice.constructor.elem);

  var newSlice = new slice.constructor(newArray);
  newSlice.$offset = newOffset;
  newSlice.$length = newLength;
  newSlice.$capacity = newCapacity;
  return newSlice;
};

var $equal = function(a, b, type) {
  if (type === $jsObjectPtr) {
    return a === b;
  }
  switch (type.kind) {
  case $kindComplex64:
  case $kindComplex128:
    return a.$real === b.$real && a.$imag === b.$imag;
  case $kindInt64:
  case $kindUint64:
    return a.$high === b.$high && a.$low === b.$low;
  case $kindPtr:
    if (a.constructor.elem) {
      return a === b;
    }
    return $pointerIsEqual(a, b);
  case $kindArray:
    if (a.length != b.length) {
      return false;
    }
    for (var i = 0; i < a.length; i++) {
      if (!$equal(a[i], b[i], type.elem)) {
        return false;
      }
    }
    return true;
  case $kindStruct:
    for (var i = 0; i < type.fields.length; i++) {
      var f = type.fields[i];
      if (!$equal(a[f.prop], b[f.prop], f.typ)) {
        return false;
      }
    }
    return true;
  case $kindInterface:
    return $interfaceIsEqual(a, b);
  default:
    return a === b;
  }
};

var $interfaceIsEqual = function(a, b) {
  if (a === $ifaceNil || b === $ifaceNil) {
    return a === b;
  }
  if (a.constructor !== b.constructor) {
    return false;
  }
  if (!a.constructor.comparable) {
    $throwRuntimeError("comparing uncomparable type " + a.constructor.string);
  }
  return $equal(a.$val, b.$val, a.constructor);
};

var $pointerIsEqual = function(a, b) {
  if (a === b) {
    return true;
  }
  if (a.$get === $throwNilPointerError || b.$get === $throwNilPointerError) {
    return a.$get === $throwNilPointerError && b.$get === $throwNilPointerError;
  }
  var va = a.$get();
  var vb = b.$get();
  if (va !== vb) {
    return false;
  }
  var dummy = va + 1;
  a.$set(dummy);
  var equal = b.$get() === dummy;
  a.$set(va);
  return equal;
};

var $kindBool = 1;
var $kindInt = 2;
var $kindInt8 = 3;
var $kindInt16 = 4;
var $kindInt32 = 5;
var $kindInt64 = 6;
var $kindUint = 7;
var $kindUint8 = 8;
var $kindUint16 = 9;
var $kindUint32 = 10;
var $kindUint64 = 11;
var $kindUintptr = 12;
var $kindFloat32 = 13;
var $kindFloat64 = 14;
var $kindComplex64 = 15;
var $kindComplex128 = 16;
var $kindArray = 17;
var $kindChan = 18;
var $kindFunc = 19;
var $kindInterface = 20;
var $kindMap = 21;
var $kindPtr = 22;
var $kindSlice = 23;
var $kindString = 24;
var $kindStruct = 25;
var $kindUnsafePointer = 26;

var $methodSynthesizers = [];
var $addMethodSynthesizer = function(f) {
  if ($methodSynthesizers === null) {
    f();
    return;
  }
  $methodSynthesizers.push(f);
};
var $synthesizeMethods = function() {
  $methodSynthesizers.forEach(function(f) { f(); });
  $methodSynthesizers = null;
};

var $newType = function(size, kind, string, name, pkg, constructor) {
  var typ;
  switch(kind) {
  case $kindBool:
  case $kindInt:
  case $kindInt8:
  case $kindInt16:
  case $kindInt32:
  case $kindUint:
  case $kindUint8:
  case $kindUint16:
  case $kindUint32:
  case $kindUintptr:
  case $kindString:
  case $kindUnsafePointer:
    typ = function(v) { this.$val = v; };
    typ.prototype.$key = function() { return string + "$" + this.$val; };
    break;

  case $kindFloat32:
  case $kindFloat64:
    typ = function(v) { this.$val = v; };
    typ.prototype.$key = function() { return string + "$" + $floatKey(this.$val); };
    break;

  case $kindInt64:
    typ = function(high, low) {
      this.$high = (high + Math.floor(Math.ceil(low) / 4294967296)) >> 0;
      this.$low = low >>> 0;
      this.$val = this;
    };
    typ.prototype.$key = function() { return string + "$" + this.$high + "$" + this.$low; };
    break;

  case $kindUint64:
    typ = function(high, low) {
      this.$high = (high + Math.floor(Math.ceil(low) / 4294967296)) >>> 0;
      this.$low = low >>> 0;
      this.$val = this;
    };
    typ.prototype.$key = function() { return string + "$" + this.$high + "$" + this.$low; };
    break;

  case $kindComplex64:
    typ = function(real, imag) {
      this.$real = $fround(real);
      this.$imag = $fround(imag);
      this.$val = this;
    };
    typ.prototype.$key = function() { return string + "$" + this.$real + "$" + this.$imag; };
    break;

  case $kindComplex128:
    typ = function(real, imag) {
      this.$real = real;
      this.$imag = imag;
      this.$val = this;
    };
    typ.prototype.$key = function() { return string + "$" + this.$real + "$" + this.$imag; };
    break;

  case $kindArray:
    typ = function(v) { this.$val = v; };
    typ.ptr = $newType(4, $kindPtr, "*" + string, "", "", function(array) {
      this.$get = function() { return array; };
      this.$set = function(v) { $copy(this, v, typ); };
      this.$val = array;
    });
    typ.init = function(elem, len) {
      typ.elem = elem;
      typ.len = len;
      typ.comparable = elem.comparable;
      typ.prototype.$key = function() {
        return string + "$" + Array.prototype.join.call($mapArray(this.$val, function(e) {
          var key = e.$key ? e.$key() : String(e);
          return key.replace(/\\/g, "\\\\").replace(/\$/g, "\\$");
        }), "$");
      };
      typ.ptr.init(typ);
      Object.defineProperty(typ.ptr.nil, "nilCheck", { get: $throwNilPointerError });
    };
    break;

  case $kindChan:
    typ = function(capacity) {
      this.$val = this;
      this.$capacity = capacity;
      this.$buffer = [];
      this.$sendQueue = [];
      this.$recvQueue = [];
      this.$closed = false;
    };
    typ.prototype.$key = function() {
      if (this.$id === undefined) {
        $idCounter++;
        this.$id = $idCounter;
      }
      return String(this.$id);
    };
    typ.init = function(elem, sendOnly, recvOnly) {
      typ.elem = elem;
      typ.sendOnly = sendOnly;
      typ.recvOnly = recvOnly;
      typ.nil = new typ(0);
      typ.nil.$sendQueue = typ.nil.$recvQueue = { length: 0, push: function() {}, shift: function() { return undefined; }, indexOf: function() { return -1; } };
    };
    break;

  case $kindFunc:
    typ = function(v) { this.$val = v; };
    typ.init = function(params, results, variadic) {
      typ.params = params;
      typ.results = results;
      typ.variadic = variadic;
      typ.comparable = false;
    };
    break;

  case $kindInterface:
    typ = { implementedBy: {}, missingMethodFor: {} };
    typ.init = function(methods) {
      typ.methods = methods;
      methods.forEach(function(m) {
        $ifaceNil[m.prop] = $throwNilPointerError;
      });
    };
    break;

  case $kindMap:
    typ = function(v) { this.$val = v; };
    typ.init = function(key, elem) {
      typ.key = key;
      typ.elem = elem;
      typ.comparable = false;
    };
    break;

  case $kindPtr:
    typ = constructor || function(getter, setter, target) {
      this.$get = getter;
      this.$set = setter;
      this.$target = target;
      this.$val = this;
    };
    typ.prototype.$key = function() {
      if (this.$id === undefined) {
        $idCounter++;
        this.$id = $idCounter;
      }
      return String(this.$id);
    };
    typ.init = function(elem) {
      typ.elem = elem;
      typ.nil = new typ($throwNilPointerError, $throwNilPointerError);
    };
    break;

  case $kindSlice:
    typ = function(array) {
      if (array.constructor !== typ.nativeArray) {
        array = new typ.nativeArray(array);
      }
      this.$array = array;
      this.$offset = 0;
      this.$length = array.length;
      this.$capacity = array.length;
      this.$val = this;
    };
    typ.init = function(elem) {
      typ.elem = elem;
      typ.comparable = false;
      typ.nativeArray = $nativeArray(elem.kind);
      typ.nil = new typ([]);
    };
    break;

  case $kindStruct:
    typ = function(v) { this.$val = v; };
    typ.ptr = $newType(4, $kindPtr, "*" + string, "", "", constructor);
    typ.ptr.elem = typ;
    typ.ptr.prototype.$get = function() { return this; };
    typ.ptr.prototype.$set = function(v) { $copy(this, v, typ); };
    typ.init = function(fields) {
      typ.fields = fields;
      fields.forEach(function(f) {
        if (!f.typ.comparable) {
          typ.comparable = false;
        }
      });
      typ.prototype.$key = function() {
        var val = this.$val;
        return string + "$" + $mapArray(fields, function(f) {
          var e = val[f.prop];
          var key = e.$key ? e.$key() : String(e);
          return key.replace(/\\/g, "\\\\").replace(/\$/g, "\\$");
        }).join("$");
      };
      /* nil value */
      var properties = {};
      fields.forEach(function(f) {
        properties[f.prop] = { get: $throwNilPointerError, set: $throwNilPointerError };
      });
      typ.ptr.nil = Object.create(constructor.prototype, properties);
      typ.ptr.nil.$val = typ.ptr.nil;
      /* methods for embedded fields */
      $addMethodSynthesizer(function() {
        var synthesizeMethod = function(target, m, f) {
          if (target.prototype[m.prop] !== undefined) { return; }
          target.prototype[m.prop] = function() {
            var v = this.$val[f.prop];
            if (f.typ === $jsObjectPtr) {
              v = new $jsObjectPtr(v);
            }
            if (v.$val === undefined) {
              v = new f.typ(v);
            }
            return v[m.prop].apply(v, arguments);
          };
        };
        fields.forEach(function(f) {
          if (f.name === "") {
            $methodSet(f.typ).forEach(function(m) {
              synthesizeMethod(typ, m, f);
              synthesizeMethod(typ.ptr, m, f);
            });
            $methodSet($ptrType(f.typ)).forEach(function(m) {
              synthesizeMethod(typ.ptr, m, f);
            });
          }
        });
      });
    };
    break;

  default:
    $panic(new $String("invalid kind: " + kind));
  }

  switch (kind) {
  case $kindBool:
  case $kindMap:
    typ.zero = function() { return false; };
    break;

  case $kindInt:
  case $kindInt8:
  case $kindInt16:
  case $kindInt32:
  case $kindUint:
  case $kindUint8 :
  case $kindUint16:
  case $kindUint32:
  case $kindUintptr:
  case $kindUnsafePointer:
  case $kindFloat32:
  case $kindFloat64:
    typ.zero = function() { return 0; };
    break;

  case $kindString:
    typ.zero = function() { return ""; };
    break;

  case $kindInt64:
  case $kindUint64:
  case $kindComplex64:
  case $kindComplex128:
    var zero = new typ(0, 0);
    typ.zero = function() { return zero; };
    break;

  case $kindChan:
  case $kindPtr:
  case $kindSlice:
    typ.zero = function() { return typ.nil; };
    break;

  case $kindFunc:
    typ.zero = function() { return $throwNilPointerError; };
    break;

  case $kindInterface:
    typ.zero = function() { return $ifaceNil; };
    break;

  case $kindArray:
    typ.zero = function() {
      var arrayClass = $nativeArray(typ.elem.kind);
      if (arrayClass !== Array) {
        return new arrayClass(typ.len);
      }
      var array = new Array(typ.len);
      for (var i = 0; i < typ.len; i++) {
        array[i] = typ.elem.zero();
      }
      return array;
    };
    break;

  case $kindStruct:
    typ.zero = function() { return new typ.ptr(); };
    break;

  default:
    $panic(new $String("invalid kind: " + kind));
  }

  typ.size = size;
  typ.kind = kind;
  typ.string = string;
  typ.typeName = name;
  typ.pkg = pkg;
  typ.methods = [];
  typ.methodSetCache = null;
  typ.comparable = true;
  return typ;
};

var $methodSet = function(typ) {
  if (typ.methodSetCache !== null) {
    return typ.methodSetCache;
  }
  var base = {};

  var isPtr = (typ.kind === $kindPtr);
  if (isPtr && typ.elem.kind === $kindInterface) {
    typ.methodSetCache = [];
    return [];
  }

  var current = [{typ: isPtr ? typ.elem : typ, indirect: isPtr}];

  var seen = {};

  while (current.length > 0) {
    var next = [];
    var mset = [];

    current.forEach(function(e) {
      if (seen[e.typ.string]) {
        return;
      }
      seen[e.typ.string] = true;

      if(e.typ.typeName !== "") {
        mset = mset.concat(e.typ.methods);
        if (e.indirect) {
          mset = mset.concat($ptrType(e.typ).methods);
        }
      }

      switch (e.typ.kind) {
      case $kindStruct:
        e.typ.fields.forEach(function(f) {
          if (f.name === "") {
            var fTyp = f.typ;
            var fIsPtr = (fTyp.kind === $kindPtr);
            next.push({typ: fIsPtr ? fTyp.elem : fTyp, indirect: e.indirect || fIsPtr});
          }
        });
        break;

      case $kindInterface:
        mset = mset.concat(e.typ.methods);
        break;
      }
    });

    mset.forEach(function(m) {
      if (base[m.name] === undefined) {
        base[m.name] = m;
      }
    });

    current = next;
  }

  typ.methodSetCache = [];
  Object.keys(base).sort().forEach(function(name) {
    typ.methodSetCache.push(base[name]);
  });
  return typ.methodSetCache;
};

var $Bool          = $newType( 1, $kindBool,          "bool",           "bool",       "", null);
var $Int           = $newType( 4, $kindInt,           "int",            "int",        "", null);
var $Int8          = $newType( 1, $kindInt8,          "int8",           "int8",       "", null);
var $Int16         = $newType( 2, $kindInt16,         "int16",          "int16",      "", null);
var $Int32         = $newType( 4, $kindInt32,         "int32",          "int32",      "", null);
var $Int64         = $newType( 8, $kindInt64,         "int64",          "int64",      "", null);
var $Uint          = $newType( 4, $kindUint,          "uint",           "uint",       "", null);
var $Uint8         = $newType( 1, $kindUint8,         "uint8",          "uint8",      "", null);
var $Uint16        = $newType( 2, $kindUint16,        "uint16",         "uint16",     "", null);
var $Uint32        = $newType( 4, $kindUint32,        "uint32",         "uint32",     "", null);
var $Uint64        = $newType( 8, $kindUint64,        "uint64",         "uint64",     "", null);
var $Uintptr       = $newType( 4, $kindUintptr,       "uintptr",        "uintptr",    "", null);
var $Float32       = $newType( 4, $kindFloat32,       "float32",        "float32",    "", null);
var $Float64       = $newType( 8, $kindFloat64,       "float64",        "float64",    "", null);
var $Complex64     = $newType( 8, $kindComplex64,     "complex64",      "complex64",  "", null);
var $Complex128    = $newType(16, $kindComplex128,    "complex128",     "complex128", "", null);
var $String        = $newType( 8, $kindString,        "string",         "string",     "", null);
var $UnsafePointer = $newType( 4, $kindUnsafePointer, "unsafe.Pointer", "Pointer",    "", null);

var $nativeArray = function(elemKind) {
  switch (elemKind) {
  case $kindInt:
    return Int32Array;
  case $kindInt8:
    return Int8Array;
  case $kindInt16:
    return Int16Array;
  case $kindInt32:
    return Int32Array;
  case $kindUint:
    return Uint32Array;
  case $kindUint8:
    return Uint8Array;
  case $kindUint16:
    return Uint16Array;
  case $kindUint32:
    return Uint32Array;
  case $kindUintptr:
    return Uint32Array;
  case $kindFloat32:
    return Float32Array;
  case $kindFloat64:
    return Float64Array;
  default:
    return Array;
  }
};
var $toNativeArray = function(elemKind, array) {
  var nativeArray = $nativeArray(elemKind);
  if (nativeArray === Array) {
    return array;
  }
  return new nativeArray(array);
};
var $arrayTypes = {};
var $arrayType = function(elem, len) {
  var string = "[" + len + "]" + elem.string;
  var typ = $arrayTypes[string];
  if (typ === undefined) {
    typ = $newType(12, $kindArray, string, "", "", null);
    $arrayTypes[string] = typ;
    typ.init(elem, len);
  }
  return typ;
};

var $chanType = function(elem, sendOnly, recvOnly) {
  var string = (recvOnly ? "<-" : "") + "chan" + (sendOnly ? "<- " : " ") + elem.string;
  var field = sendOnly ? "SendChan" : (recvOnly ? "RecvChan" : "Chan");
  var typ = elem[field];
  if (typ === undefined) {
    typ = $newType(4, $kindChan, string, "", "", null);
    elem[field] = typ;
    typ.init(elem, sendOnly, recvOnly);
  }
  return typ;
};

var $funcTypes = {};
var $funcType = function(params, results, variadic) {
  var paramTypes = $mapArray(params, function(p) { return p.string; });
  if (variadic) {
    paramTypes[paramTypes.length - 1] = "..." + paramTypes[paramTypes.length - 1].substr(2);
  }
  var string = "func(" + paramTypes.join(", ") + ")";
  if (results.length === 1) {
    string += " " + results[0].string;
  } else if (results.length > 1) {
    string += " (" + $mapArray(results, function(r) { return r.string; }).join(", ") + ")";
  }
  var typ = $funcTypes[string];
  if (typ === undefined) {
    typ = $newType(4, $kindFunc, string, "", "", null);
    $funcTypes[string] = typ;
    typ.init(params, results, variadic);
  }
  return typ;
};

var $interfaceTypes = {};
var $interfaceType = function(methods) {
  var string = "interface {}";
  if (methods.length !== 0) {
    string = "interface { " + $mapArray(methods, function(m) {
      return (m.pkg !== "" ? m.pkg + "." : "") + m.name + m.typ.string.substr(4);
    }).join("; ") + " }";
  }
  var typ = $interfaceTypes[string];
  if (typ === undefined) {
    typ = $newType(8, $kindInterface, string, "", "", null);
    $interfaceTypes[string] = typ;
    typ.init(methods);
  }
  return typ;
};
var $emptyInterface = $interfaceType([]);
var $ifaceNil = { $key: function() { return "nil"; } };
var $error = $newType(8, $kindInterface, "error", "error", "", null);
$error.init([{prop: "Error", name: "Error", pkg: "", typ: $funcType([], [$String], false)}]);

var $Map = function() {};
(function() {
  var names = Object.getOwnPropertyNames(Object.prototype);
  for (var i = 0; i < names.length; i++) {
    $Map.prototype[names[i]] = undefined;
  }
})();
var $mapTypes = {};
var $mapType = function(key, elem) {
  var string = "map[" + key.string + "]" + elem.string;
  var typ = $mapTypes[string];
  if (typ === undefined) {
    typ = $newType(4, $kindMap, string, "", "", null);
    $mapTypes[string] = typ;
    typ.init(key, elem);
  }
  return typ;
};

var $ptrType = function(elem) {
  var typ = elem.ptr;
  if (typ === undefined) {
    typ = $newType(4, $kindPtr, "*" + elem.string, "", "", null);
    elem.ptr = typ;
    typ.init(elem);
  }
  return typ;
};

var $newDataPointer = function(data, constructor) {
  if (constructor.elem.kind === $kindStruct) {
    return data;
  }
  return new constructor(function() { return data; }, function(v) { data = v; });
};

var $sliceType = function(elem) {
  var typ = elem.Slice;
  if (typ === undefined) {
    typ = $newType(12, $kindSlice, "[]" + elem.string, "", "", null);
    elem.Slice = typ;
    typ.init(elem);
  }
  return typ;
};
var $makeSlice = function(typ, length, capacity) {
  capacity = capacity || length;
  var array = new typ.nativeArray(capacity);
  if (typ.nativeArray === Array) {
    for (var i = 0; i < capacity; i++) {
      array[i] = typ.elem.zero();
    }
  }
  var slice = new typ(array);
  slice.$length = length;
  return slice;
};

var $structTypes = {};
var $structType = function(fields) {
  var string = "struct { " + $mapArray(fields, function(f) {
    return f.name + " " + f.typ.string + (f.tag !== "" ? (" \"" + f.tag.replace(/\\/g, "\\\\").replace(/"/g, "\\\"") + "\"") : "");
  }).join("; ") + " }";
  if (fields.length === 0) {
    string = "struct {}";
  }
  var typ = $structTypes[string];
  if (typ === undefined) {
    typ = $newType(0, $kindStruct, string, "", "", function() {
      this.$val = this;
      for (var i = 0; i < fields.length; i++) {
        var f = fields[i];
        var arg = arguments[i];
        this[f.prop] = arg !== undefined ? arg : f.typ.zero();
      }
    });
    $structTypes[string] = typ;
    typ.init(fields);
  }
  return typ;
};

var $assertType = function(value, type, returnTuple) {
  var isInterface = (type.kind === $kindInterface), ok, missingMethod = "";
  if (value === $ifaceNil) {
    ok = false;
  } else if (!isInterface) {
    ok = value.constructor === type;
  } else {
    var valueTypeString = value.constructor.string;
    ok = type.implementedBy[valueTypeString];
    if (ok === undefined) {
      ok = true;
      var valueMethodSet = $methodSet(value.constructor);
      var interfaceMethods = type.methods;
      for (var i = 0; i < interfaceMethods.length; i++) {
        var tm = interfaceMethods[i];
        var found = false;
        for (var j = 0; j < valueMethodSet.length; j++) {
          var vm = valueMethodSet[j];
          if (vm.name === tm.name && vm.pkg === tm.pkg && vm.typ === tm.typ) {
            found = true;
            break;
          }
        }
        if (!found) {
          ok = false;
          type.missingMethodFor[valueTypeString] = tm.name;
          break;
        }
      }
      type.implementedBy[valueTypeString] = ok;
    }
    if (!ok) {
      missingMethod = type.missingMethodFor[valueTypeString];
    }
  }

  if (!ok) {
    if (returnTuple) {
      return [type.zero(), false];
    }
    $panic(new $packages["runtime"].TypeAssertionError.ptr("", (value === $ifaceNil ? "" : value.constructor.string), type.string, missingMethod));
  }

  if (!isInterface) {
    value = value.$val;
  }
  if (type === $jsObjectPtr) {
    value = value.object;
  }
  return returnTuple ? [value, true] : value;
};

var $floatKey = function(f) {
  if (f !== f) {
    $idCounter++;
    return "NaN$" + $idCounter;
  }
  return String(f);
};

var $flatten64 = function(x) {
  return x.$high * 4294967296 + x.$low;
};

var $shiftLeft64 = function(x, y) {
  if (y === 0) {
    return x;
  }
  if (y < 32) {
    return new x.constructor(x.$high << y | x.$low >>> (32 - y), (x.$low << y) >>> 0);
  }
  if (y < 64) {
    return new x.constructor(x.$low << (y - 32), 0);
  }
  return new x.constructor(0, 0);
};

var $shiftRightInt64 = function(x, y) {
  if (y === 0) {
    return x;
  }
  if (y < 32) {
    return new x.constructor(x.$high >> y, (x.$low >>> y | x.$high << (32 - y)) >>> 0);
  }
  if (y < 64) {
    return new x.constructor(x.$high >> 31, (x.$high >> (y - 32)) >>> 0);
  }
  if (x.$high < 0) {
    return new x.constructor(-1, 4294967295);
  }
  return new x.constructor(0, 0);
};

var $shiftRightUint64 = function(x, y) {
  if (y === 0) {
    return x;
  }
  if (y < 32) {
    return new x.constructor(x.$high >>> y, (x.$low >>> y | x.$high << (32 - y)) >>> 0);
  }
  if (y < 64) {
    return new x.constructor(0, x.$high >>> (y - 32));
  }
  return new x.constructor(0, 0);
};

var $mul64 = function(x, y) {
  var high = 0, low = 0;
  if ((y.$low & 1) !== 0) {
    high = x.$high;
    low = x.$low;
  }
  for (var i = 1; i < 32; i++) {
    if ((y.$low & 1<<i) !== 0) {
      high += x.$high << i | x.$low >>> (32 - i);
      low += (x.$low << i) >>> 0;
    }
  }
  for (var i = 0; i < 32; i++) {
    if ((y.$high & 1<<i) !== 0) {
      high += x.$low << i;
    }
  }
  return new x.constructor(high, low);
};

var $div64 = function(x, y, returnRemainder) {
  if (y.$high === 0 && y.$low === 0) {
    $throwRuntimeError("integer divide by zero");
  }

  var s = 1;
  var rs = 1;

  var xHigh = x.$high;
  var xLow = x.$low;
  if (xHigh < 0) {
    s = -1;
    rs = -1;
    xHigh = -xHigh;
    if (xLow !== 0) {
      xHigh--;
      xLow = 4294967296 - xLow;
    }
  }

  var yHigh = y.$high;
  var yLow = y.$low;
  if (y.$high < 0) {
    s *= -1;
    yHigh = -yHigh;
    if (yLow !== 0) {
      yHigh--;
      yLow = 4294967296 - yLow;
    }
  }

  var high = 0, low = 0, n = 0;
  while (yHigh < 2147483648 && ((xHigh > yHigh) || (xHigh === yHigh && xLow > yLow))) {
    yHigh = (yHigh << 1 | yLow >>> 31) >>> 0;
    yLow = (yLow << 1) >>> 0;
    n++;
  }
  for (var i = 0; i <= n; i++) {
    high = high << 1 | low >>> 31;
    low = (low << 1) >>> 0;
    if ((xHigh > yHigh) || (xHigh === yHigh && xLow >= yLow)) {
      xHigh = xHigh - yHigh;
      xLow = xLow - yLow;
      if (xLow < 0) {
        xHigh--;
        xLow += 4294967296;
      }
      low++;
      if (low === 4294967296) {
        high++;
        low = 0;
      }
    }
    yLow = (yLow >>> 1 | yHigh << (32 - 1)) >>> 0;
    yHigh = yHigh >>> 1;
  }

  if (returnRemainder) {
    return new x.constructor(xHigh * rs, xLow * rs);
  }
  return new x.constructor(high * s, low * s);
};

var $divComplex = function(n, d) {
  var ninf = n.$real === 1/0 || n.$real === -1/0 || n.$imag === 1/0 || n.$imag === -1/0;
  var dinf = d.$real === 1/0 || d.$real === -1/0 || d.$imag === 1/0 || d.$imag === -1/0;
  var nnan = !ninf && (n.$real !== n.$real || n.$imag !== n.$imag);
  var dnan = !dinf && (d.$real !== d.$real || d.$imag !== d.$imag);
  if(nnan || dnan) {
    return new n.constructor(0/0, 0/0);
  }
  if (ninf && !dinf) {
    return new n.constructor(1/0, 1/0);
  }
  if (!ninf && dinf) {
    return new n.constructor(0, 0);
  }
  if (d.$real === 0 && d.$imag === 0) {
    if (n.$real === 0 && n.$imag === 0) {
      return new n.constructor(0/0, 0/0);
    }
    return new n.constructor(1/0, 1/0);
  }
  var a = Math.abs(d.$real);
  var b = Math.abs(d.$imag);
  if (a <= b) {
    var ratio = d.$real / d.$imag;
    var denom = d.$real * ratio + d.$imag;
    return new n.constructor((n.$real * ratio + n.$imag) / denom, (n.$imag * ratio - n.$real) / denom);
  }
  var ratio = d.$imag / d.$real;
  var denom = d.$imag * ratio + d.$real;
  return new n.constructor((n.$imag * ratio + n.$real) / denom, (n.$imag - n.$real * ratio) / denom);
};

var $stackDepthOffset = 0;
var $getStackDepth = function() {
  var err = new Error();
  if (err.stack === undefined) {
    return undefined;
  }
  return $stackDepthOffset + err.stack.split("\n").length;
};

var $deferFrames = [], $skippedDeferFrames = 0, $jumpToDefer = false, $panicStackDepth = null, $panicValue;
var $callDeferred = function(deferred, jsErr) {
  if ($skippedDeferFrames !== 0) {
    $skippedDeferFrames--;
    throw jsErr;
  }
  if ($jumpToDefer) {
    $jumpToDefer = false;
    throw jsErr;
  }
  if (jsErr) {
    var newErr = null;
    try {
      $deferFrames.push(deferred);
      $panic(new $jsErrorPtr(jsErr));
    } catch (err) {
      newErr = err;
    }
    $deferFrames.pop();
    $callDeferred(deferred, newErr);
    return;
  }

  $stackDepthOffset--;
  var outerPanicStackDepth = $panicStackDepth;
  var outerPanicValue = $panicValue;

  var localPanicValue = $curGoroutine.panicStack.pop();
  if (localPanicValue !== undefined) {
    $panicStackDepth = $getStackDepth();
    $panicValue = localPanicValue;
  }

  var call, localSkippedDeferFrames = 0;
  try {
    while (true) {
      if (deferred === null) {
        deferred = $deferFrames[$deferFrames.length - 1 - localSkippedDeferFrames];
        if (deferred === undefined) {
          if (localPanicValue.Object instanceof Error) {
            throw localPanicValue.Object;
          }
          var msg;
          if (localPanicValue.constructor === $String) {
            msg = localPanicValue.$val;
          } else if (localPanicValue.Error !== undefined) {
            msg = localPanicValue.Error($BLOCKING);
            if (msg && msg.$blocking) { msg = msg(); }
          } else if (localPanicValue.String !== undefined) {
            msg = localPanicValue.String($BLOCKING);
            if (msg && msg.$blocking) { msg = msg(); }
          } else {
            msg = localPanicValue;
          }
          throw new Error(msg);
        }
      }
      var call = deferred.pop();
      if (call === undefined) {
        if (localPanicValue !== undefined) {
          localSkippedDeferFrames++;
          deferred = null;
          continue;
        }
        return;
      }
      var r = call[0].apply(undefined, call[1]);
      if (r && r.$blocking) {
        deferred.push([r, []]);
      }

      if (localPanicValue !== undefined && $panicStackDepth === null) {
        throw null; /* error was recovered */
      }
    }
  } finally {
    $skippedDeferFrames += localSkippedDeferFrames;
    if ($curGoroutine.asleep) {
      deferred.push(call);
      $jumpToDefer = true;
    }
    if (localPanicValue !== undefined) {
      if ($panicStackDepth !== null) {
        $curGoroutine.panicStack.push(localPanicValue);
      }
      $panicStackDepth = outerPanicStackDepth;
      $panicValue = outerPanicValue;
    }
    $stackDepthOffset++;
  }
};

var $panic = function(value) {
  $curGoroutine.panicStack.push(value);
  $callDeferred(null, null);
};
var $recover = function() {
  if ($panicStackDepth === null || ($panicStackDepth !== undefined && $panicStackDepth !== $getStackDepth() - 2)) {
    return $ifaceNil;
  }
  $panicStackDepth = null;
  return $panicValue;
};
var $throw = function(err) { throw err; };

var $BLOCKING = new Object();
var $nonblockingCall = function() {
  $panic(new $packages["runtime"].NotSupportedError.ptr("non-blocking call to blocking function, see https://github.com/gopherjs/gopherjs#goroutines"));
};

var $dummyGoroutine = { asleep: false, exit: false, panicStack: [] };
var $curGoroutine = $dummyGoroutine, $totalGoroutines = 0, $awakeGoroutines = 0, $checkForDeadlock = true;
var $go = function(fun, args, direct) {
  $totalGoroutines++;
  $awakeGoroutines++;
  args.push($BLOCKING);
  var goroutine = function() {
    var rescheduled = false;
    try {
      $curGoroutine = goroutine;
      $skippedDeferFrames = 0;
      $jumpToDefer = false;
      var r = fun.apply(undefined, args);
      if (r && r.$blocking) {
        fun = r;
        args = [];
        $schedule(goroutine, direct);
        rescheduled = true;
        return;
      }
      goroutine.exit = true;
    } catch (err) {
      if (!$curGoroutine.asleep) {
        goroutine.exit = true;
        throw err;
      }
    } finally {
      $curGoroutine = $dummyGoroutine;
      if (goroutine.exit && !rescheduled) { /* also set by runtime.Goexit() */
        $totalGoroutines--;
        goroutine.asleep = true;
      }
      if (goroutine.asleep && !rescheduled) {
        $awakeGoroutines--;
        if ($awakeGoroutines === 0 && $totalGoroutines !== 0 && $checkForDeadlock) {
          console.error("fatal error: all goroutines are asleep - deadlock!");
        }
      }
    }
  };
  goroutine.asleep = false;
  goroutine.exit = false;
  goroutine.panicStack = [];
  $schedule(goroutine, direct);
};

var $scheduled = [], $schedulerLoopActive = false;
var $schedule = function(goroutine, direct) {
  if (goroutine.asleep) {
    goroutine.asleep = false;
    $awakeGoroutines++;
  }

  if (direct) {
    goroutine();
    return;
  }

  $scheduled.push(goroutine);
  if (!$schedulerLoopActive) {
    $schedulerLoopActive = true;
    setTimeout(function() {
      while (true) {
        var r = $scheduled.shift();
        if (r === undefined) {
          $schedulerLoopActive = false;
          break;
        }
        r();
      };
    }, 0);
  }
};

var $send = function(chan, value) {
  if (chan.$closed) {
    $throwRuntimeError("send on closed channel");
  }
  var queuedRecv = chan.$recvQueue.shift();
  if (queuedRecv !== undefined) {
    queuedRecv([value, true]);
    return;
  }
  if (chan.$buffer.length < chan.$capacity) {
    chan.$buffer.push(value);
    return;
  }

  var thisGoroutine = $curGoroutine;
  chan.$sendQueue.push(function() {
    $schedule(thisGoroutine);
    return value;
  });
  var blocked = false;
  var f = function() {
    if (blocked) {
      if (chan.$closed) {
        $throwRuntimeError("send on closed channel");
      }
      return;
    };
    blocked = true;
    $curGoroutine.asleep = true;
    throw null;
  };
  f.$blocking = true;
  return f;
};
var $recv = function(chan) {
  var queuedSend = chan.$sendQueue.shift();
  if (queuedSend !== undefined) {
    chan.$buffer.push(queuedSend());
  }
  var bufferedValue = chan.$buffer.shift();
  if (bufferedValue !== undefined) {
    return [bufferedValue, true];
  }
  if (chan.$closed) {
    return [chan.constructor.elem.zero(), false];
  }

  var thisGoroutine = $curGoroutine, value;
  var queueEntry = function(v) {
    value = v;
    $schedule(thisGoroutine);
  };
  chan.$recvQueue.push(queueEntry);
  var blocked = false;
  var f = function() {
    if (blocked) {
      return value;
    };
    blocked = true;
    $curGoroutine.asleep = true;
    throw null;
  };
  f.$blocking = true;
  return f;
};
var $close = function(chan) {
  if (chan.$closed) {
    $throwRuntimeError("close of closed channel");
  }
  chan.$closed = true;
  while (true) {
    var queuedSend = chan.$sendQueue.shift();
    if (queuedSend === undefined) {
      break;
    }
    queuedSend(); /* will panic because of closed channel */
  }
  while (true) {
    var queuedRecv = chan.$recvQueue.shift();
    if (queuedRecv === undefined) {
      break;
    }
    queuedRecv([chan.constructor.elem.zero(), false]);
  }
};
var $select = function(comms) {
  var ready = [];
  var selection = -1;
  for (var i = 0; i < comms.length; i++) {
    var comm = comms[i];
    var chan = comm[0];
    switch (comm.length) {
    case 0: /* default */
      selection = i;
      break;
    case 1: /* recv */
      if (chan.$sendQueue.length !== 0 || chan.$buffer.length !== 0 || chan.$closed) {
        ready.push(i);
      }
      break;
    case 2: /* send */
      if (chan.$closed) {
        $throwRuntimeError("send on closed channel");
      }
      if (chan.$recvQueue.length !== 0 || chan.$buffer.length < chan.$capacity) {
        ready.push(i);
      }
      break;
    }
  }

  if (ready.length !== 0) {
    selection = ready[Math.floor(Math.random() * ready.length)];
  }
  if (selection !== -1) {
    var comm = comms[selection];
    switch (comm.length) {
    case 0: /* default */
      return [selection];
    case 1: /* recv */
      return [selection, $recv(comm[0])];
    case 2: /* send */
      $send(comm[0], comm[1]);
      return [selection];
    }
  }

  var entries = [];
  var thisGoroutine = $curGoroutine;
  var removeFromQueues = function() {
    for (var i = 0; i < entries.length; i++) {
      var entry = entries[i];
      var queue = entry[0];
      var index = queue.indexOf(entry[1]);
      if (index !== -1) {
        queue.splice(index, 1);
      }
    }
  };
  for (var i = 0; i < comms.length; i++) {
    (function(i) {
      var comm = comms[i];
      switch (comm.length) {
      case 1: /* recv */
        var queueEntry = function(value) {
          selection = [i, value];
          removeFromQueues();
          $schedule(thisGoroutine);
        };
        entries.push([comm[0].$recvQueue, queueEntry]);
        comm[0].$recvQueue.push(queueEntry);
        break;
      case 2: /* send */
        var queueEntry = function() {
          if (comm[0].$closed) {
            $throwRuntimeError("send on closed channel");
          }
          selection = [i];
          removeFromQueues();
          $schedule(thisGoroutine);
          return comm[1];
        };
        entries.push([comm[0].$sendQueue, queueEntry]);
        comm[0].$sendQueue.push(queueEntry);
        break;
      }
    })(i);
  }
  var blocked = false;
  var f = function() {
    if (blocked) {
      return selection;
    };
    blocked = true;
    $curGoroutine.asleep = true;
    throw null;
  };
  f.$blocking = true;
  return f;
};

var $jsObjectPtr, $jsErrorPtr;

var $needsExternalization = function(t) {
  switch (t.kind) {
    case $kindBool:
    case $kindInt:
    case $kindInt8:
    case $kindInt16:
    case $kindInt32:
    case $kindUint:
    case $kindUint8:
    case $kindUint16:
    case $kindUint32:
    case $kindUintptr:
    case $kindFloat32:
    case $kindFloat64:
      return false;
    default:
      return t !== $jsObjectPtr;
  }
};

var $externalize = function(v, t) {
  if (t === $jsObjectPtr) {
    return v;
  }
  switch (t.kind) {
  case $kindBool:
  case $kindInt:
  case $kindInt8:
  case $kindInt16:
  case $kindInt32:
  case $kindUint:
  case $kindUint8:
  case $kindUint16:
  case $kindUint32:
  case $kindUintptr:
  case $kindFloat32:
  case $kindFloat64:
    return v;
  case $kindInt64:
  case $kindUint64:
    return $flatten64(v);
  case $kindArray:
    if ($needsExternalization(t.elem)) {
      return $mapArray(v, function(e) { return $externalize(e, t.elem); });
    }
    return v;
  case $kindFunc:
    if (v === $throwNilPointerError) {
      return null;
    }
    if (v.$externalizeWrapper === undefined) {
      $checkForDeadlock = false;
      var convert = false;
      for (var i = 0; i < t.params.length; i++) {
        convert = convert || (t.params[i] !== $jsObjectPtr);
      }
      for (var i = 0; i < t.results.length; i++) {
        convert = convert || $needsExternalization(t.results[i]);
      }
      v.$externalizeWrapper = v;
      if (convert) {
        v.$externalizeWrapper = function() {
          var args = [];
          for (var i = 0; i < t.params.length; i++) {
            if (t.variadic && i === t.params.length - 1) {
              var vt = t.params[i].elem, varargs = [];
              for (var j = i; j < arguments.length; j++) {
                varargs.push($internalize(arguments[j], vt));
              }
              args.push(new (t.params[i])(varargs));
              break;
            }
            args.push($internalize(arguments[i], t.params[i]));
          }
          var result = v.apply(this, args);
          switch (t.results.length) {
          case 0:
            return;
          case 1:
            return $externalize(result, t.results[0]);
          default:
            for (var i = 0; i < t.results.length; i++) {
              result[i] = $externalize(result[i], t.results[i]);
            }
            return result;
          }
        };
      }
    }
    return v.$externalizeWrapper;
  case $kindInterface:
    if (v === $ifaceNil) {
      return null;
    }
    if (v.constructor === $jsObjectPtr) {
      return v.$val.object;
    }
    return $externalize(v.$val, v.constructor);
  case $kindMap:
    var m = {};
    var keys = $keys(v);
    for (var i = 0; i < keys.length; i++) {
      var entry = v[keys[i]];
      m[$externalize(entry.k, t.key)] = $externalize(entry.v, t.elem);
    }
    return m;
  case $kindPtr:
    if (v === t.nil) {
      return null;
    }
    return $externalize(v.$get(), t.elem);
  case $kindSlice:
    if ($needsExternalization(t.elem)) {
      return $mapArray($sliceToArray(v), function(e) { return $externalize(e, t.elem); });
    }
    return $sliceToArray(v);
  case $kindString:
    if (v.search(/^[\x00-\x7F]*$/) !== -1) {
      return v;
    }
    var s = "", r;
    for (var i = 0; i < v.length; i += r[1]) {
      r = $decodeRune(v, i);
      s += String.fromCharCode(r[0]);
    }
    return s;
  case $kindStruct:
    var timePkg = $packages["time"];
    if (timePkg && v.constructor === timePkg.Time.ptr) {
      var milli = $div64(v.UnixNano(), new $Int64(0, 1000000));
      return new Date($flatten64(milli));
    }

    var noJsObject = {};
    var searchJsObject = function(v, t) {
      if (t === $jsObjectPtr) {
        return v;
      }
      switch (t.kind) {
      case $kindPtr:
        if (v === t.nil) {
          return noJsObject;
        }
        return searchJsObject(v.$get(), t.elem);
      case $kindStruct:
        for (var i = 0; i < t.fields.length; i++) {
          var f = t.fields[i];
          var o = searchJsObject(v[f.prop], f.typ);
          if (o !== noJsObject) {
            return o;
          }
        }
        return noJsObject;
      case $kindInterface:
        return searchJsObject(v.$val, v.constructor);
      default:
        return noJsObject;
      }
    };
    var o = searchJsObject(v, t);
    if (o !== noJsObject) {
      return o;
    }

    o = {};
    for (var i = 0; i < t.fields.length; i++) {
      var f = t.fields[i];
      if (f.pkg !== "") { /* not exported */
        continue;
      }
      o[f.name] = $externalize(v[f.prop], f.typ);
    }
    return o;
  }
  $panic(new $String("cannot externalize " + t.string));
};

var $internalize = function(v, t, recv) {
  if (t === $jsObjectPtr) {
    return v;
  }
  if (t === $jsObjectPtr.elem) {
    $panic(new $String("cannot internalize js.Object, use *js.Object instead"));
  }
  switch (t.kind) {
  case $kindBool:
    return !!v;
  case $kindInt:
    return parseInt(v);
  case $kindInt8:
    return parseInt(v) << 24 >> 24;
  case $kindInt16:
    return parseInt(v) << 16 >> 16;
  case $kindInt32:
    return parseInt(v) >> 0;
  case $kindUint:
    return parseInt(v);
  case $kindUint8:
    return parseInt(v) << 24 >>> 24;
  case $kindUint16:
    return parseInt(v) << 16 >>> 16;
  case $kindUint32:
  case $kindUintptr:
    return parseInt(v) >>> 0;
  case $kindInt64:
  case $kindUint64:
    return new t(0, v);
  case $kindFloat32:
  case $kindFloat64:
    return parseFloat(v);
  case $kindArray:
    if (v.length !== t.len) {
      $throwRuntimeError("got array with wrong size from JavaScript native");
    }
    return $mapArray(v, function(e) { return $internalize(e, t.elem); });
  case $kindFunc:
    return function() {
      var args = [];
      for (var i = 0; i < t.params.length; i++) {
        if (t.variadic && i === t.params.length - 1) {
          var vt = t.params[i].elem, varargs = arguments[i];
          for (var j = 0; j < varargs.$length; j++) {
            args.push($externalize(varargs.$array[varargs.$offset + j], vt));
          }
          break;
        }
        args.push($externalize(arguments[i], t.params[i]));
      }
      var result = v.apply(recv, args);
      switch (t.results.length) {
      case 0:
        return;
      case 1:
        return $internalize(result, t.results[0]);
      default:
        for (var i = 0; i < t.results.length; i++) {
          result[i] = $internalize(result[i], t.results[i]);
        }
        return result;
      }
    };
  case $kindInterface:
    if (t.methods.length !== 0) {
      $panic(new $String("cannot internalize " + t.string));
    }
    if (v === null) {
      return $ifaceNil;
    }
    switch (v.constructor) {
    case Int8Array:
      return new ($sliceType($Int8))(v);
    case Int16Array:
      return new ($sliceType($Int16))(v);
    case Int32Array:
      return new ($sliceType($Int))(v);
    case Uint8Array:
      return new ($sliceType($Uint8))(v);
    case Uint16Array:
      return new ($sliceType($Uint16))(v);
    case Uint32Array:
      return new ($sliceType($Uint))(v);
    case Float32Array:
      return new ($sliceType($Float32))(v);
    case Float64Array:
      return new ($sliceType($Float64))(v);
    case Array:
      return $internalize(v, $sliceType($emptyInterface));
    case Boolean:
      return new $Bool(!!v);
    case Date:
      var timePkg = $packages["time"];
      if (timePkg) {
        return new timePkg.Time(timePkg.Unix(new $Int64(0, 0), new $Int64(0, v.getTime() * 1000000)));
      }
    case Function:
      var funcType = $funcType([$sliceType($emptyInterface)], [$jsObjectPtr], true);
      return new funcType($internalize(v, funcType));
    case Number:
      return new $Float64(parseFloat(v));
    case String:
      return new $String($internalize(v, $String));
    default:
      if ($global.Node && v instanceof $global.Node) {
        return new $jsObjectPtr(v);
      }
      var mapType = $mapType($String, $emptyInterface);
      return new mapType($internalize(v, mapType));
    }
  case $kindMap:
    var m = new $Map();
    var keys = $keys(v);
    for (var i = 0; i < keys.length; i++) {
      var key = $internalize(keys[i], t.key);
      m[key.$key ? key.$key() : key] = { k: key, v: $internalize(v[keys[i]], t.elem) };
    }
    return m;
  case $kindPtr:
    if (t.elem.kind === $kindStruct) {
      return $internalize(v, t.elem);
    }
  case $kindSlice:
    return new t($mapArray(v, function(e) { return $internalize(e, t.elem); }));
  case $kindString:
    v = String(v);
    if (v.search(/^[\x00-\x7F]*$/) !== -1) {
      return v;
    }
    var s = "";
    for (var i = 0; i < v.length; i++) {
      s += $encodeRune(v.charCodeAt(i));
    }
    return s;
  case $kindStruct:
    var noJsObject = {};
    var searchJsObject = function(t) {
      if (t === $jsObjectPtr) {
        return v;
      }
      if (t === $jsObjectPtr.elem) {
        $panic(new $String("cannot internalize js.Object, use *js.Object instead"));
      }
      switch (t.kind) {
      case $kindPtr:
        return searchJsObject(t.elem);
      case $kindStruct:
        for (var i = 0; i < t.fields.length; i++) {
          var f = t.fields[i];
          var o = searchJsObject(f.typ);
          if (o !== noJsObject) {
            var n = new t.ptr();
            n[f.prop] = o;
            return n;
          }
        }
        return noJsObject;
      default:
        return noJsObject;
      }
    };
    var o = searchJsObject(t);
    if (o !== noJsObject) {
      return o;
    }
  }
  $panic(new $String("cannot internalize " + t.string));
};

$packages["github.com/gopherjs/gopherjs/js"] = (function() {
	var $pkg = {}, Object, Error, sliceType$1, sliceType$2, sliceType$3, ptrType$3, ptrType$4, ptrType$5, ptrType$6, ptrType$7, ptrType$8, ptrType$9, ptrType$10, ptrType$11, init;
	Object = $pkg.Object = $newType(0, $kindStruct, "js.Object", "Object", "github.com/gopherjs/gopherjs/js", function(object_) {
		this.$val = this;
		this.object = object_ !== undefined ? object_ : null;
	});
	Error = $pkg.Error = $newType(0, $kindStruct, "js.Error", "Error", "github.com/gopherjs/gopherjs/js", function(Object_) {
		this.$val = this;
		this.Object = Object_ !== undefined ? Object_ : null;
	});
	sliceType$1 = $sliceType($emptyInterface);
	sliceType$2 = $sliceType($emptyInterface);
	sliceType$3 = $sliceType($emptyInterface);
	ptrType$3 = $ptrType(Object);
	ptrType$4 = $ptrType(Object);
	ptrType$5 = $ptrType(Object);
	ptrType$6 = $ptrType(Object);
	ptrType$7 = $ptrType(Object);
	ptrType$8 = $ptrType(Object);
	ptrType$9 = $ptrType(Object);
	ptrType$10 = $ptrType(Error);
	ptrType$11 = $ptrType(Object);
	Object.ptr.prototype.Get = function(key) {
		var key, o;
		o = this;
		return o.object[$externalize(key, $String)];
	};
	Object.prototype.Get = function(key) { return this.$val.Get(key); };
	Object.ptr.prototype.Set = function(key, value) {
		var key, o, value;
		o = this;
		o.object[$externalize(key, $String)] = $externalize(value, $emptyInterface);
	};
	Object.prototype.Set = function(key, value) { return this.$val.Set(key, value); };
	Object.ptr.prototype.Delete = function(key) {
		var key, o;
		o = this;
		delete o.object[$externalize(key, $String)];
	};
	Object.prototype.Delete = function(key) { return this.$val.Delete(key); };
	Object.ptr.prototype.Length = function() {
		var o;
		o = this;
		return $parseInt(o.object.length);
	};
	Object.prototype.Length = function() { return this.$val.Length(); };
	Object.ptr.prototype.Index = function(i) {
		var i, o;
		o = this;
		return o.object[i];
	};
	Object.prototype.Index = function(i) { return this.$val.Index(i); };
	Object.ptr.prototype.SetIndex = function(i, value) {
		var i, o, value;
		o = this;
		o.object[i] = $externalize(value, $emptyInterface);
	};
	Object.prototype.SetIndex = function(i, value) { return this.$val.SetIndex(i, value); };
	Object.ptr.prototype.Call = function(name, args) {
		var args, name, o, obj;
		o = this;
		return (obj = o.object, obj[$externalize(name, $String)].apply(obj, $externalize(args, sliceType$1)));
	};
	Object.prototype.Call = function(name, args) { return this.$val.Call(name, args); };
	Object.ptr.prototype.Invoke = function(args) {
		var args, o;
		o = this;
		return o.object.apply(undefined, $externalize(args, sliceType$2));
	};
	Object.prototype.Invoke = function(args) { return this.$val.Invoke(args); };
	Object.ptr.prototype.New = function(args) {
		var args, o;
		o = this;
		return new ($global.Function.prototype.bind.apply(o.object, [undefined].concat($externalize(args, sliceType$3))));
	};
	Object.prototype.New = function(args) { return this.$val.New(args); };
	Object.ptr.prototype.Bool = function() {
		var o;
		o = this;
		return !!(o.object);
	};
	Object.prototype.Bool = function() { return this.$val.Bool(); };
	Object.ptr.prototype.String = function() {
		var o;
		o = this;
		return $internalize(o.object, $String);
	};
	Object.prototype.String = function() { return this.$val.String(); };
	Object.ptr.prototype.Int = function() {
		var o;
		o = this;
		return $parseInt(o.object) >> 0;
	};
	Object.prototype.Int = function() { return this.$val.Int(); };
	Object.ptr.prototype.Int64 = function() {
		var o;
		o = this;
		return $internalize(o.object, $Int64);
	};
	Object.prototype.Int64 = function() { return this.$val.Int64(); };
	Object.ptr.prototype.Uint64 = function() {
		var o;
		o = this;
		return $internalize(o.object, $Uint64);
	};
	Object.prototype.Uint64 = function() { return this.$val.Uint64(); };
	Object.ptr.prototype.Float = function() {
		var o;
		o = this;
		return $parseFloat(o.object);
	};
	Object.prototype.Float = function() { return this.$val.Float(); };
	Object.ptr.prototype.Interface = function() {
		var o;
		o = this;
		return $internalize(o.object, $emptyInterface);
	};
	Object.prototype.Interface = function() { return this.$val.Interface(); };
	Object.ptr.prototype.Unsafe = function() {
		var o;
		o = this;
		return o.object;
	};
	Object.prototype.Unsafe = function() { return this.$val.Unsafe(); };
	Error.ptr.prototype.Error = function() {
		var err;
		err = this;
		return "JavaScript error: " + $internalize(err.Object.message, $String);
	};
	Error.prototype.Error = function() { return this.$val.Error(); };
	Error.ptr.prototype.Stack = function() {
		var err;
		err = this;
		return $internalize(err.Object.stack, $String);
	};
	Error.prototype.Stack = function() { return this.$val.Stack(); };
	init = function() {
		var e;
		e = new Error.ptr(null);
	};
	ptrType$8.methods = [{prop: "Get", name: "Get", pkg: "", typ: $funcType([$String], [ptrType$3], false)}, {prop: "Set", name: "Set", pkg: "", typ: $funcType([$String, $emptyInterface], [], false)}, {prop: "Delete", name: "Delete", pkg: "", typ: $funcType([$String], [], false)}, {prop: "Length", name: "Length", pkg: "", typ: $funcType([], [$Int], false)}, {prop: "Index", name: "Index", pkg: "", typ: $funcType([$Int], [ptrType$4], false)}, {prop: "SetIndex", name: "SetIndex", pkg: "", typ: $funcType([$Int, $emptyInterface], [], false)}, {prop: "Call", name: "Call", pkg: "", typ: $funcType([$String, sliceType$1], [ptrType$5], true)}, {prop: "Invoke", name: "Invoke", pkg: "", typ: $funcType([sliceType$2], [ptrType$6], true)}, {prop: "New", name: "New", pkg: "", typ: $funcType([sliceType$3], [ptrType$7], true)}, {prop: "Bool", name: "Bool", pkg: "", typ: $funcType([], [$Bool], false)}, {prop: "String", name: "String", pkg: "", typ: $funcType([], [$String], false)}, {prop: "Int", name: "Int", pkg: "", typ: $funcType([], [$Int], false)}, {prop: "Int64", name: "Int64", pkg: "", typ: $funcType([], [$Int64], false)}, {prop: "Uint64", name: "Uint64", pkg: "", typ: $funcType([], [$Uint64], false)}, {prop: "Float", name: "Float", pkg: "", typ: $funcType([], [$Float64], false)}, {prop: "Interface", name: "Interface", pkg: "", typ: $funcType([], [$emptyInterface], false)}, {prop: "Unsafe", name: "Unsafe", pkg: "", typ: $funcType([], [$Uintptr], false)}];
	ptrType$10.methods = [{prop: "Error", name: "Error", pkg: "", typ: $funcType([], [$String], false)}, {prop: "Stack", name: "Stack", pkg: "", typ: $funcType([], [$String], false)}];
	Object.init([{prop: "object", name: "object", pkg: "github.com/gopherjs/gopherjs/js", typ: ptrType$9, tag: ""}]);
	Error.init([{prop: "Object", name: "", pkg: "", typ: ptrType$11, tag: ""}]);
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_js = function() { while (true) { switch ($s) { case 0:
		init();
		/* */ } return; } }; $init_js.$blocking = true; return $init_js;
	};
	return $pkg;
})();
$packages["runtime"] = (function() {
	var $pkg = {}, js, NotSupportedError, TypeAssertionError, errorString, ptrType$7, ptrType$9, init, GOROOT;
	js = $packages["github.com/gopherjs/gopherjs/js"];
	NotSupportedError = $pkg.NotSupportedError = $newType(0, $kindStruct, "runtime.NotSupportedError", "NotSupportedError", "runtime", function(Feature_) {
		this.$val = this;
		this.Feature = Feature_ !== undefined ? Feature_ : "";
	});
	TypeAssertionError = $pkg.TypeAssertionError = $newType(0, $kindStruct, "runtime.TypeAssertionError", "TypeAssertionError", "runtime", function(interfaceString_, concreteString_, assertedString_, missingMethod_) {
		this.$val = this;
		this.interfaceString = interfaceString_ !== undefined ? interfaceString_ : "";
		this.concreteString = concreteString_ !== undefined ? concreteString_ : "";
		this.assertedString = assertedString_ !== undefined ? assertedString_ : "";
		this.missingMethod = missingMethod_ !== undefined ? missingMethod_ : "";
	});
	errorString = $pkg.errorString = $newType(8, $kindString, "runtime.errorString", "errorString", "runtime", null);
	ptrType$7 = $ptrType(NotSupportedError);
	ptrType$9 = $ptrType(TypeAssertionError);
	NotSupportedError.ptr.prototype.Error = function() {
		var err;
		err = this;
		return "not supported by GopherJS: " + err.Feature;
	};
	NotSupportedError.prototype.Error = function() { return this.$val.Error(); };
	init = function() {
		var e, jsPkg;
		jsPkg = $packages[$externalize("github.com/gopherjs/gopherjs/js", $String)];
		$jsObjectPtr = jsPkg.Object.ptr;
		$jsErrorPtr = jsPkg.Error.ptr;
		$throwRuntimeError = (function(msg) {
			var msg;
			$panic(new errorString(msg));
		});
		e = $ifaceNil;
		e = new TypeAssertionError.ptr("", "", "", "");
		e = new NotSupportedError.ptr("");
	};
	GOROOT = $pkg.GOROOT = function() {
		var goroot, process;
		process = $global.process;
		if (process === undefined) {
			return "/";
		}
		goroot = process.env.GOROOT;
		if (!(goroot === undefined)) {
			return $internalize(goroot, $String);
		}
		return "/usr/local/go";
	};
	TypeAssertionError.ptr.prototype.RuntimeError = function() {
	};
	TypeAssertionError.prototype.RuntimeError = function() { return this.$val.RuntimeError(); };
	TypeAssertionError.ptr.prototype.Error = function() {
		var e, inter;
		e = this;
		inter = e.interfaceString;
		if (inter === "") {
			inter = "interface";
		}
		if (e.concreteString === "") {
			return "interface conversion: " + inter + " is nil, not " + e.assertedString;
		}
		if (e.missingMethod === "") {
			return "interface conversion: " + inter + " is " + e.concreteString + ", not " + e.assertedString;
		}
		return "interface conversion: " + e.concreteString + " is not " + e.assertedString + ": missing method " + e.missingMethod;
	};
	TypeAssertionError.prototype.Error = function() { return this.$val.Error(); };
	errorString.prototype.RuntimeError = function() {
		var e;
		e = this.$val;
	};
	$ptrType(errorString).prototype.RuntimeError = function() { return new errorString(this.$get()).RuntimeError(); };
	errorString.prototype.Error = function() {
		var e;
		e = this.$val;
		return "runtime error: " + e;
	};
	$ptrType(errorString).prototype.Error = function() { return new errorString(this.$get()).Error(); };
	ptrType$7.methods = [{prop: "Error", name: "Error", pkg: "", typ: $funcType([], [$String], false)}];
	ptrType$9.methods = [{prop: "RuntimeError", name: "RuntimeError", pkg: "", typ: $funcType([], [], false)}, {prop: "Error", name: "Error", pkg: "", typ: $funcType([], [$String], false)}];
	errorString.methods = [{prop: "RuntimeError", name: "RuntimeError", pkg: "", typ: $funcType([], [], false)}, {prop: "Error", name: "Error", pkg: "", typ: $funcType([], [$String], false)}];
	NotSupportedError.init([{prop: "Feature", name: "Feature", pkg: "", typ: $String, tag: ""}]);
	TypeAssertionError.init([{prop: "interfaceString", name: "interfaceString", pkg: "runtime", typ: $String, tag: ""}, {prop: "concreteString", name: "concreteString", pkg: "runtime", typ: $String, tag: ""}, {prop: "assertedString", name: "assertedString", pkg: "runtime", typ: $String, tag: ""}, {prop: "missingMethod", name: "missingMethod", pkg: "runtime", typ: $String, tag: ""}]);
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_runtime = function() { while (true) { switch ($s) { case 0:
		$r = js.$init($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		init();
		/* */ } return; } }; $init_runtime.$blocking = true; return $init_runtime;
	};
	return $pkg;
})();
$packages["github.com/albrow/jquery"] = (function() {
	var $pkg = {}, js, JQuery, Event, JQueryCoordinates, Deferred, sliceType$1, sliceType$2, sliceType$3, sliceType$4, sliceType$5, funcType, funcType$1, sliceType$6, sliceType$7, sliceType$8, sliceType$9, sliceType$10, sliceType$11, sliceType$12, sliceType$13, sliceType$14, mapType, sliceType$15, sliceType$16, funcType$2, sliceType$17, sliceType$18, funcType$3, sliceType$19, sliceType$20, sliceType$21, sliceType$22, funcType$4, sliceType$23, sliceType$24, sliceType$25, funcType$5, sliceType$26, sliceType$27, sliceType$28, sliceType$29, sliceType$30, sliceType$31, sliceType$32, sliceType$33, sliceType$34, sliceType$35, sliceType$36, sliceType$37, sliceType$38, sliceType$39, sliceType$40, funcType$6, sliceType$41, sliceType$42, sliceType$43, sliceType$44, sliceType$45, sliceType$46, sliceType$47, sliceType$48, sliceType$49, sliceType$50, mapType$2, sliceType$51, mapType$3, sliceType$52, sliceType$53, sliceType$54, sliceType$55, sliceType$56, sliceType$57, sliceType$58, sliceType$59, sliceType$60, sliceType$61, sliceType$62, sliceType$63, ptrType, ptrType$1, sliceType$64, mapType$4, sliceType$65, sliceType$66, ptrType$2, ptrType$3, ptrType$4, ptrType$5, ptrType$6, ptrType$7, ptrType$8, ptrType$9, ptrType$10, ptrType$11, ptrType$12, ptrType$13, NewJQuery, Trim, GlobalEval, Type, IsPlainObject, IsEmptyObject, IsFunction, IsNumeric, IsXMLDoc, IsWindow, InArray, ParseHTML, ParseXML, ParseJSON, Grep, Noop, Now, Unique, Ajax, AjaxPrefilter, AjaxSetup, AjaxTransport, Get, Post, GetJSON, GetScript, When, NewDeferred;
	js = $packages["github.com/gopherjs/gopherjs/js"];
	JQuery = $pkg.JQuery = $newType(0, $kindStruct, "jquery.JQuery", "JQuery", "github.com/albrow/jquery", function(o_, Jquery_, Selector_, Length_, Context_) {
		this.$val = this;
		this.o = o_ !== undefined ? o_ : null;
		this.Jquery = Jquery_ !== undefined ? Jquery_ : "";
		this.Selector = Selector_ !== undefined ? Selector_ : "";
		this.Length = Length_ !== undefined ? Length_ : 0;
		this.Context = Context_ !== undefined ? Context_ : "";
	});
	Event = $pkg.Event = $newType(0, $kindStruct, "jquery.Event", "Event", "github.com/albrow/jquery", function(Object_, KeyCode_, Target_, CurrentTarget_, DelegateTarget_, RelatedTarget_, Data_, Result_, Which_, Namespace_, MetaKey_, PageX_, PageY_, Type_) {
		this.$val = this;
		this.Object = Object_ !== undefined ? Object_ : null;
		this.KeyCode = KeyCode_ !== undefined ? KeyCode_ : 0;
		this.Target = Target_ !== undefined ? Target_ : null;
		this.CurrentTarget = CurrentTarget_ !== undefined ? CurrentTarget_ : null;
		this.DelegateTarget = DelegateTarget_ !== undefined ? DelegateTarget_ : null;
		this.RelatedTarget = RelatedTarget_ !== undefined ? RelatedTarget_ : null;
		this.Data = Data_ !== undefined ? Data_ : null;
		this.Result = Result_ !== undefined ? Result_ : null;
		this.Which = Which_ !== undefined ? Which_ : 0;
		this.Namespace = Namespace_ !== undefined ? Namespace_ : "";
		this.MetaKey = MetaKey_ !== undefined ? MetaKey_ : false;
		this.PageX = PageX_ !== undefined ? PageX_ : 0;
		this.PageY = PageY_ !== undefined ? PageY_ : 0;
		this.Type = Type_ !== undefined ? Type_ : "";
	});
	JQueryCoordinates = $pkg.JQueryCoordinates = $newType(0, $kindStruct, "jquery.JQueryCoordinates", "JQueryCoordinates", "github.com/albrow/jquery", function(Left_, Top_) {
		this.$val = this;
		this.Left = Left_ !== undefined ? Left_ : 0;
		this.Top = Top_ !== undefined ? Top_ : 0;
	});
	Deferred = $pkg.Deferred = $newType(0, $kindStruct, "jquery.Deferred", "Deferred", "github.com/albrow/jquery", function(Object_) {
		this.$val = this;
		this.Object = Object_ !== undefined ? Object_ : null;
	});
	sliceType$1 = $sliceType($emptyInterface);
	sliceType$2 = $sliceType($emptyInterface);
	sliceType$3 = $sliceType($emptyInterface);
	sliceType$4 = $sliceType($emptyInterface);
	sliceType$5 = $sliceType($emptyInterface);
	funcType = $funcType([$emptyInterface, $Int], [$Bool], false);
	funcType$1 = $funcType([$Int, $emptyInterface], [], false);
	sliceType$6 = $sliceType($emptyInterface);
	sliceType$7 = $sliceType($emptyInterface);
	sliceType$8 = $sliceType($emptyInterface);
	sliceType$9 = $sliceType($emptyInterface);
	sliceType$10 = $sliceType($emptyInterface);
	sliceType$11 = $sliceType($emptyInterface);
	sliceType$12 = $sliceType($emptyInterface);
	sliceType$13 = $sliceType($emptyInterface);
	sliceType$14 = $sliceType($emptyInterface);
	mapType = $mapType($String, $emptyInterface);
	sliceType$15 = $sliceType($String);
	sliceType$16 = $sliceType($emptyInterface);
	funcType$2 = $funcType([$Int, $String], [$String], false);
	sliceType$17 = $sliceType($emptyInterface);
	sliceType$18 = $sliceType($emptyInterface);
	funcType$3 = $funcType([$Int, $String], [$String], false);
	sliceType$19 = $sliceType($emptyInterface);
	sliceType$20 = $sliceType($emptyInterface);
	sliceType$21 = $sliceType($emptyInterface);
	sliceType$22 = $sliceType($emptyInterface);
	funcType$4 = $funcType([$Int, $String], [$String], false);
	sliceType$23 = $sliceType($emptyInterface);
	sliceType$24 = $sliceType($emptyInterface);
	sliceType$25 = $sliceType($emptyInterface);
	funcType$5 = $funcType([$Int, $String], [$String], false);
	sliceType$26 = $sliceType($emptyInterface);
	sliceType$27 = $sliceType($emptyInterface);
	sliceType$28 = $sliceType($emptyInterface);
	sliceType$29 = $sliceType($emptyInterface);
	sliceType$30 = $sliceType($emptyInterface);
	sliceType$31 = $sliceType($emptyInterface);
	sliceType$32 = $sliceType($emptyInterface);
	sliceType$33 = $sliceType($emptyInterface);
	sliceType$34 = $sliceType($emptyInterface);
	sliceType$35 = $sliceType($emptyInterface);
	sliceType$36 = $sliceType($emptyInterface);
	sliceType$37 = $sliceType($emptyInterface);
	sliceType$38 = $sliceType($emptyInterface);
	sliceType$39 = $sliceType($emptyInterface);
	sliceType$40 = $sliceType($emptyInterface);
	funcType$6 = $funcType([], [], false);
	sliceType$41 = $sliceType($emptyInterface);
	sliceType$42 = $sliceType($emptyInterface);
	sliceType$43 = $sliceType($emptyInterface);
	sliceType$44 = $sliceType($emptyInterface);
	sliceType$45 = $sliceType($emptyInterface);
	sliceType$46 = $sliceType($emptyInterface);
	sliceType$47 = $sliceType($emptyInterface);
	sliceType$48 = $sliceType($emptyInterface);
	sliceType$49 = $sliceType($emptyInterface);
	sliceType$50 = $sliceType($emptyInterface);
	mapType$2 = $mapType($String, $emptyInterface);
	sliceType$51 = $sliceType($emptyInterface);
	mapType$3 = $mapType($String, $emptyInterface);
	sliceType$52 = $sliceType($emptyInterface);
	sliceType$53 = $sliceType($emptyInterface);
	sliceType$54 = $sliceType($emptyInterface);
	sliceType$55 = $sliceType($emptyInterface);
	sliceType$56 = $sliceType($emptyInterface);
	sliceType$57 = $sliceType($emptyInterface);
	sliceType$58 = $sliceType($emptyInterface);
	sliceType$59 = $sliceType($emptyInterface);
	sliceType$60 = $sliceType($emptyInterface);
	sliceType$61 = $sliceType($emptyInterface);
	sliceType$62 = $sliceType($emptyInterface);
	sliceType$63 = $sliceType($emptyInterface);
	ptrType = $ptrType(js.Object);
	ptrType$1 = $ptrType(js.Object);
	sliceType$64 = $sliceType($emptyInterface);
	mapType$4 = $mapType($String, $emptyInterface);
	sliceType$65 = $sliceType($Bool);
	sliceType$66 = $sliceType($Bool);
	ptrType$2 = $ptrType(js.Object);
	ptrType$3 = $ptrType(js.Object);
	ptrType$4 = $ptrType(Event);
	ptrType$5 = $ptrType(js.Object);
	ptrType$6 = $ptrType(js.Object);
	ptrType$7 = $ptrType(js.Object);
	ptrType$8 = $ptrType(js.Object);
	ptrType$9 = $ptrType(js.Object);
	ptrType$10 = $ptrType(js.Object);
	ptrType$11 = $ptrType(js.Object);
	ptrType$12 = $ptrType(js.Object);
	ptrType$13 = $ptrType(js.Object);
	Event.ptr.prototype.PreventDefault = function() {
		var event;
		event = this;
		event.Object.preventDefault();
	};
	Event.prototype.PreventDefault = function() { return this.$val.PreventDefault(); };
	Event.ptr.prototype.IsDefaultPrevented = function() {
		var event;
		event = this;
		return !!(event.Object.isDefaultPrevented());
	};
	Event.prototype.IsDefaultPrevented = function() { return this.$val.IsDefaultPrevented(); };
	Event.ptr.prototype.IsImmediatePropogationStopped = function() {
		var event;
		event = this;
		return !!(event.Object.isImmediatePropogationStopped());
	};
	Event.prototype.IsImmediatePropogationStopped = function() { return this.$val.IsImmediatePropogationStopped(); };
	Event.ptr.prototype.IsPropagationStopped = function() {
		var event;
		event = this;
		return !!(event.Object.isPropagationStopped());
	};
	Event.prototype.IsPropagationStopped = function() { return this.$val.IsPropagationStopped(); };
	Event.ptr.prototype.StopImmediatePropagation = function() {
		var event;
		event = this;
		event.Object.stopImmediatePropagation();
	};
	Event.prototype.StopImmediatePropagation = function() { return this.$val.StopImmediatePropagation(); };
	Event.ptr.prototype.StopPropagation = function() {
		var event;
		event = this;
		event.Object.stopPropagation();
	};
	Event.prototype.StopPropagation = function() { return this.$val.StopPropagation(); };
	NewJQuery = $pkg.NewJQuery = function(args) {
		var args;
		return new JQuery.ptr(new ($global.Function.prototype.bind.apply($global.jQuery, [undefined].concat($externalize(args, sliceType$1)))), "", "", 0, "");
	};
	Trim = $pkg.Trim = function(text) {
		var text;
		return $internalize($global.jQuery.trim($externalize(text, $String)), $String);
	};
	GlobalEval = $pkg.GlobalEval = function(cmd) {
		var cmd;
		$global.jQuery.globalEval($externalize(cmd, $String));
	};
	Type = $pkg.Type = function(sth) {
		var sth;
		return $internalize($global.jQuery.type($externalize(sth, $emptyInterface)), $String);
	};
	IsPlainObject = $pkg.IsPlainObject = function(sth) {
		var sth;
		return !!($global.jQuery.isPlainObject($externalize(sth, $emptyInterface)));
	};
	IsEmptyObject = $pkg.IsEmptyObject = function(sth) {
		var sth;
		return !!($global.jQuery.isEmptyObject($externalize(sth, $emptyInterface)));
	};
	IsFunction = $pkg.IsFunction = function(sth) {
		var sth;
		return !!($global.jQuery.isFunction($externalize(sth, $emptyInterface)));
	};
	IsNumeric = $pkg.IsNumeric = function(sth) {
		var sth;
		return !!($global.jQuery.isNumeric($externalize(sth, $emptyInterface)));
	};
	IsXMLDoc = $pkg.IsXMLDoc = function(sth) {
		var sth;
		return !!($global.jQuery.isXMLDoc($externalize(sth, $emptyInterface)));
	};
	IsWindow = $pkg.IsWindow = function(sth) {
		var sth;
		return !!($global.jQuery.isWindow($externalize(sth, $emptyInterface)));
	};
	InArray = $pkg.InArray = function(val, arr) {
		var arr, val;
		return $parseInt($global.jQuery.inArray($externalize(val, $emptyInterface), $externalize(arr, sliceType$2))) >> 0;
	};
	ParseHTML = $pkg.ParseHTML = function(text) {
		var text;
		return $assertType($internalize($global.jQuery.parseHTML($externalize(text, $String)), $emptyInterface), sliceType$3);
	};
	ParseXML = $pkg.ParseXML = function(text) {
		var text;
		return $internalize($global.jQuery.parseXML($externalize(text, $String)), $emptyInterface);
	};
	ParseJSON = $pkg.ParseJSON = function(text) {
		var text;
		return $internalize($global.jQuery.parseJSON($externalize(text, $String)), $emptyInterface);
	};
	Grep = $pkg.Grep = function(arr, fn) {
		var arr, fn;
		return $assertType($internalize($global.jQuery.grep($externalize(arr, sliceType$5), $externalize(fn, funcType)), $emptyInterface), sliceType$4);
	};
	Noop = $pkg.Noop = function() {
		return $internalize($global.jQuery.noop, $emptyInterface);
	};
	Now = $pkg.Now = function() {
		return $parseFloat($global.jQuery.now());
	};
	Unique = $pkg.Unique = function(arr) {
		var arr;
		return $global.jQuery.unique(arr);
	};
	JQuery.ptr.prototype.Each = function(fn) {
		var fn, j;
		j = $clone(this, JQuery);
		j.o = j.o.each($externalize(fn, funcType$1));
		return j;
	};
	JQuery.prototype.Each = function(fn) { return this.$val.Each(fn); };
	JQuery.ptr.prototype.Underlying = function() {
		var j;
		j = $clone(this, JQuery);
		return j.o;
	};
	JQuery.prototype.Underlying = function() { return this.$val.Underlying(); };
	JQuery.ptr.prototype.Get = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		return (obj = j.o, obj.get.apply(obj, $externalize(i, sliceType$6)));
	};
	JQuery.prototype.Get = function(i) { return this.$val.Get(i); };
	JQuery.ptr.prototype.Append = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.append.apply(obj, $externalize(i, sliceType$7)));
		return j;
	};
	JQuery.prototype.Append = function(i) { return this.$val.Append(i); };
	JQuery.ptr.prototype.Empty = function() {
		var j;
		j = $clone(this, JQuery);
		j.o = j.o.empty();
		return j;
	};
	JQuery.prototype.Empty = function() { return this.$val.Empty(); };
	JQuery.ptr.prototype.Detach = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.detach.apply(obj, $externalize(i, sliceType$8)));
		return j;
	};
	JQuery.prototype.Detach = function(i) { return this.$val.Detach(i); };
	JQuery.ptr.prototype.Eq = function(idx) {
		var idx, j;
		j = $clone(this, JQuery);
		j.o = j.o.eq(idx);
		return j;
	};
	JQuery.prototype.Eq = function(idx) { return this.$val.Eq(idx); };
	JQuery.ptr.prototype.FadeIn = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.fadeIn.apply(obj, $externalize(i, sliceType$9)));
		return j;
	};
	JQuery.prototype.FadeIn = function(i) { return this.$val.FadeIn(i); };
	JQuery.ptr.prototype.Delay = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.delay.apply(obj, $externalize(i, sliceType$10)));
		return j;
	};
	JQuery.prototype.Delay = function(i) { return this.$val.Delay(i); };
	JQuery.ptr.prototype.ToArray = function() {
		var j;
		j = $clone(this, JQuery);
		return $assertType($internalize(j.o.toArray(), $emptyInterface), sliceType$11);
	};
	JQuery.prototype.ToArray = function() { return this.$val.ToArray(); };
	JQuery.ptr.prototype.Remove = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.remove.apply(obj, $externalize(i, sliceType$12)));
		return j;
	};
	JQuery.prototype.Remove = function(i) { return this.$val.Remove(i); };
	JQuery.ptr.prototype.Stop = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.stop.apply(obj, $externalize(i, sliceType$13)));
		return j;
	};
	JQuery.prototype.Stop = function(i) { return this.$val.Stop(i); };
	JQuery.ptr.prototype.AddBack = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.addBack.apply(obj, $externalize(i, sliceType$14)));
		return j;
	};
	JQuery.prototype.AddBack = function(i) { return this.$val.AddBack(i); };
	JQuery.ptr.prototype.Css = function(name) {
		var j, name;
		j = $clone(this, JQuery);
		return $internalize(j.o.css($externalize(name, $String)), $String);
	};
	JQuery.prototype.Css = function(name) { return this.$val.Css(name); };
	JQuery.ptr.prototype.CssArray = function(arr) {
		var arr, j;
		j = $clone(this, JQuery);
		return $assertType($internalize(j.o.css($externalize(arr, sliceType$15)), $emptyInterface), mapType);
	};
	JQuery.prototype.CssArray = function(arr) { return this.$val.CssArray(arr); };
	JQuery.ptr.prototype.SetCss = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.css.apply(obj, $externalize(i, sliceType$16)));
		return j;
	};
	JQuery.prototype.SetCss = function(i) { return this.$val.SetCss(i); };
	JQuery.ptr.prototype.Text = function() {
		var j;
		j = $clone(this, JQuery);
		return $internalize(j.o.text(), $String);
	};
	JQuery.prototype.Text = function() { return this.$val.Text(); };
	JQuery.ptr.prototype.SetText = function(i) {
		var _ref, i, j;
		j = $clone(this, JQuery);
		_ref = i;
		if ($assertType(_ref, funcType$2, true)[1] || $assertType(_ref, $String, true)[1]) {
		} else {
			console.log("SetText Argument should be 'string' or 'func(int, string) string'");
		}
		j.o = j.o.text($externalize(i, $emptyInterface));
		return j;
	};
	JQuery.prototype.SetText = function(i) { return this.$val.SetText(i); };
	JQuery.ptr.prototype.Val = function() {
		var j;
		j = $clone(this, JQuery);
		return $internalize(j.o.val(), $String);
	};
	JQuery.prototype.Val = function() { return this.$val.Val(); };
	JQuery.ptr.prototype.SetVal = function(i) {
		var i, j;
		j = $clone(this, JQuery);
		j.o.val($externalize(i, $emptyInterface));
		return j;
	};
	JQuery.prototype.SetVal = function(i) { return this.$val.SetVal(i); };
	JQuery.ptr.prototype.Prop = function(property) {
		var j, property;
		j = $clone(this, JQuery);
		return $internalize(j.o.prop($externalize(property, $String)), $emptyInterface);
	};
	JQuery.prototype.Prop = function(property) { return this.$val.Prop(property); };
	JQuery.ptr.prototype.SetProp = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.prop.apply(obj, $externalize(i, sliceType$17)));
		return j;
	};
	JQuery.prototype.SetProp = function(i) { return this.$val.SetProp(i); };
	JQuery.ptr.prototype.RemoveProp = function(property) {
		var j, property;
		j = $clone(this, JQuery);
		j.o = j.o.removeProp($externalize(property, $String));
		return j;
	};
	JQuery.prototype.RemoveProp = function(property) { return this.$val.RemoveProp(property); };
	JQuery.ptr.prototype.Attr = function(property) {
		var attr, j, property;
		j = $clone(this, JQuery);
		attr = j.o.attr($externalize(property, $String));
		if (attr === undefined) {
			return "";
		}
		return $internalize(attr, $String);
	};
	JQuery.prototype.Attr = function(property) { return this.$val.Attr(property); };
	JQuery.ptr.prototype.SetAttr = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.attr.apply(obj, $externalize(i, sliceType$18)));
		return j;
	};
	JQuery.prototype.SetAttr = function(i) { return this.$val.SetAttr(i); };
	JQuery.ptr.prototype.RemoveAttr = function(property) {
		var j, property;
		j = $clone(this, JQuery);
		j.o = j.o.removeAttr($externalize(property, $String));
		return j;
	};
	JQuery.prototype.RemoveAttr = function(property) { return this.$val.RemoveAttr(property); };
	JQuery.ptr.prototype.HasClass = function(class$1) {
		var class$1, j;
		j = $clone(this, JQuery);
		return !!(j.o.hasClass($externalize(class$1, $String)));
	};
	JQuery.prototype.HasClass = function(class$1) { return this.$val.HasClass(class$1); };
	JQuery.ptr.prototype.AddClass = function(i) {
		var _ref, i, j;
		j = $clone(this, JQuery);
		_ref = i;
		if ($assertType(_ref, funcType$3, true)[1] || $assertType(_ref, $String, true)[1]) {
		} else {
			console.log("addClass Argument should be 'string' or 'func(int, string) string'");
		}
		j.o = j.o.addClass($externalize(i, $emptyInterface));
		return j;
	};
	JQuery.prototype.AddClass = function(i) { return this.$val.AddClass(i); };
	JQuery.ptr.prototype.RemoveClass = function(property) {
		var j, property;
		j = $clone(this, JQuery);
		j.o = j.o.removeClass($externalize(property, $String));
		return j;
	};
	JQuery.prototype.RemoveClass = function(property) { return this.$val.RemoveClass(property); };
	JQuery.ptr.prototype.ToggleClass = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.toggleClass.apply(obj, $externalize(i, sliceType$19)));
		return j;
	};
	JQuery.prototype.ToggleClass = function(i) { return this.$val.ToggleClass(i); };
	JQuery.ptr.prototype.Focus = function() {
		var j;
		j = $clone(this, JQuery);
		j.o = j.o.focus();
		return j;
	};
	JQuery.prototype.Focus = function() { return this.$val.Focus(); };
	JQuery.ptr.prototype.Blur = function() {
		var j;
		j = $clone(this, JQuery);
		j.o = j.o.blur();
		return j;
	};
	JQuery.prototype.Blur = function() { return this.$val.Blur(); };
	JQuery.ptr.prototype.ReplaceAll = function(i) {
		var i, j;
		j = $clone(this, JQuery);
		j.o = j.o.replaceAll($externalize(i, $emptyInterface));
		return j;
	};
	JQuery.prototype.ReplaceAll = function(i) { return this.$val.ReplaceAll(i); };
	JQuery.ptr.prototype.ReplaceWith = function(i) {
		var i, j;
		j = $clone(this, JQuery);
		j.o = j.o.replaceWith($externalize(i, $emptyInterface));
		return j;
	};
	JQuery.prototype.ReplaceWith = function(i) { return this.$val.ReplaceWith(i); };
	JQuery.ptr.prototype.After = function(i) {
		var i, j;
		j = $clone(this, JQuery);
		j.o = j.o.after($externalize(i, sliceType$20));
		return j;
	};
	JQuery.prototype.After = function(i) { return this.$val.After(i); };
	JQuery.ptr.prototype.Before = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.before.apply(obj, $externalize(i, sliceType$21)));
		return j;
	};
	JQuery.prototype.Before = function(i) { return this.$val.Before(i); };
	JQuery.ptr.prototype.Prepend = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.prepend.apply(obj, $externalize(i, sliceType$22)));
		return j;
	};
	JQuery.prototype.Prepend = function(i) { return this.$val.Prepend(i); };
	JQuery.ptr.prototype.PrependTo = function(i) {
		var i, j;
		j = $clone(this, JQuery);
		j.o = j.o.prependTo($externalize(i, $emptyInterface));
		return j;
	};
	JQuery.prototype.PrependTo = function(i) { return this.$val.PrependTo(i); };
	JQuery.ptr.prototype.AppendTo = function(i) {
		var i, j;
		j = $clone(this, JQuery);
		j.o = j.o.appendTo($externalize(i, $emptyInterface));
		return j;
	};
	JQuery.prototype.AppendTo = function(i) { return this.$val.AppendTo(i); };
	JQuery.ptr.prototype.InsertAfter = function(i) {
		var i, j;
		j = $clone(this, JQuery);
		j.o = j.o.insertAfter($externalize(i, $emptyInterface));
		return j;
	};
	JQuery.prototype.InsertAfter = function(i) { return this.$val.InsertAfter(i); };
	JQuery.ptr.prototype.InsertBefore = function(i) {
		var i, j;
		j = $clone(this, JQuery);
		j.o = j.o.insertBefore($externalize(i, $emptyInterface));
		return j;
	};
	JQuery.prototype.InsertBefore = function(i) { return this.$val.InsertBefore(i); };
	JQuery.ptr.prototype.Show = function() {
		var j;
		j = $clone(this, JQuery);
		j.o = j.o.show();
		return j;
	};
	JQuery.prototype.Show = function() { return this.$val.Show(); };
	JQuery.ptr.prototype.Hide = function() {
		var j;
		j = $clone(this, JQuery);
		j.o.hide();
		return j;
	};
	JQuery.prototype.Hide = function() { return this.$val.Hide(); };
	JQuery.ptr.prototype.Toggle = function(showOrHide) {
		var j, showOrHide;
		j = $clone(this, JQuery);
		j.o = j.o.toggle($externalize(showOrHide, $Bool));
		return j;
	};
	JQuery.prototype.Toggle = function(showOrHide) { return this.$val.Toggle(showOrHide); };
	JQuery.ptr.prototype.Contents = function() {
		var j;
		j = $clone(this, JQuery);
		j.o = j.o.contents();
		return j;
	};
	JQuery.prototype.Contents = function() { return this.$val.Contents(); };
	JQuery.ptr.prototype.Html = function() {
		var j;
		j = $clone(this, JQuery);
		return $internalize(j.o.html(), $String);
	};
	JQuery.prototype.Html = function() { return this.$val.Html(); };
	JQuery.ptr.prototype.SetHtml = function(i) {
		var _ref, i, j;
		j = $clone(this, JQuery);
		_ref = i;
		if ($assertType(_ref, funcType$4, true)[1] || $assertType(_ref, $String, true)[1]) {
		} else {
			console.log("SetHtml Argument should be 'string' or 'func(int, string) string'");
		}
		j.o = j.o.html($externalize(i, $emptyInterface));
		return j;
	};
	JQuery.prototype.SetHtml = function(i) { return this.$val.SetHtml(i); };
	JQuery.ptr.prototype.Closest = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.closest.apply(obj, $externalize(i, sliceType$23)));
		return j;
	};
	JQuery.prototype.Closest = function(i) { return this.$val.Closest(i); };
	JQuery.ptr.prototype.End = function() {
		var j;
		j = $clone(this, JQuery);
		j.o = j.o.end();
		return j;
	};
	JQuery.prototype.End = function() { return this.$val.End(); };
	JQuery.ptr.prototype.Add = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.add.apply(obj, $externalize(i, sliceType$24)));
		return j;
	};
	JQuery.prototype.Add = function(i) { return this.$val.Add(i); };
	JQuery.ptr.prototype.Clone = function(b) {
		var b, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.clone.apply(obj, $externalize(b, sliceType$25)));
		return j;
	};
	JQuery.prototype.Clone = function(b) { return this.$val.Clone(b); };
	JQuery.ptr.prototype.Height = function() {
		var j;
		j = $clone(this, JQuery);
		return $parseInt(j.o.height()) >> 0;
	};
	JQuery.prototype.Height = function() { return this.$val.Height(); };
	JQuery.ptr.prototype.SetHeight = function(value) {
		var j, value;
		j = $clone(this, JQuery);
		j.o = j.o.height($externalize(value, $String));
		return j;
	};
	JQuery.prototype.SetHeight = function(value) { return this.$val.SetHeight(value); };
	JQuery.ptr.prototype.Width = function() {
		var j;
		j = $clone(this, JQuery);
		return $parseInt(j.o.width()) >> 0;
	};
	JQuery.prototype.Width = function() { return this.$val.Width(); };
	JQuery.ptr.prototype.SetWidth = function(i) {
		var _ref, i, j;
		j = $clone(this, JQuery);
		_ref = i;
		if ($assertType(_ref, funcType$5, true)[1] || $assertType(_ref, $String, true)[1]) {
		} else {
			console.log("SetWidth Argument should be 'string' or 'func(int, string) string'");
		}
		j.o = j.o.width($externalize(i, $emptyInterface));
		return j;
	};
	JQuery.prototype.SetWidth = function(i) { return this.$val.SetWidth(i); };
	JQuery.ptr.prototype.InnerHeight = function() {
		var j;
		j = $clone(this, JQuery);
		return $parseInt(j.o.innerHeight()) >> 0;
	};
	JQuery.prototype.InnerHeight = function() { return this.$val.InnerHeight(); };
	JQuery.ptr.prototype.InnerWidth = function() {
		var j;
		j = $clone(this, JQuery);
		return $parseInt(j.o.innerWidth()) >> 0;
	};
	JQuery.prototype.InnerWidth = function() { return this.$val.InnerWidth(); };
	JQuery.ptr.prototype.Offset = function() {
		var j, obj;
		j = $clone(this, JQuery);
		obj = j.o.offset();
		return new JQueryCoordinates.ptr($parseInt(obj.left) >> 0, $parseInt(obj.top) >> 0);
	};
	JQuery.prototype.Offset = function() { return this.$val.Offset(); };
	JQuery.ptr.prototype.SetOffset = function(jc) {
		var j, jc;
		j = $clone(this, JQuery);
		jc = $clone(jc, JQueryCoordinates);
		j.o = j.o.offset($externalize(jc, JQueryCoordinates));
		return j;
	};
	JQuery.prototype.SetOffset = function(jc) { return this.$val.SetOffset(jc); };
	JQuery.ptr.prototype.OuterHeight = function(includeMargin) {
		var includeMargin, j;
		j = $clone(this, JQuery);
		if (includeMargin.$length === 0) {
			return $parseInt(j.o.outerHeight()) >> 0;
		}
		return $parseInt(j.o.outerHeight($externalize((0 >= includeMargin.$length ? $throwRuntimeError("index out of range") : includeMargin.$array[includeMargin.$offset + 0]), $Bool))) >> 0;
	};
	JQuery.prototype.OuterHeight = function(includeMargin) { return this.$val.OuterHeight(includeMargin); };
	JQuery.ptr.prototype.OuterWidth = function(includeMargin) {
		var includeMargin, j;
		j = $clone(this, JQuery);
		if (includeMargin.$length === 0) {
			return $parseInt(j.o.outerWidth()) >> 0;
		}
		return $parseInt(j.o.outerWidth($externalize((0 >= includeMargin.$length ? $throwRuntimeError("index out of range") : includeMargin.$array[includeMargin.$offset + 0]), $Bool))) >> 0;
	};
	JQuery.prototype.OuterWidth = function(includeMargin) { return this.$val.OuterWidth(includeMargin); };
	JQuery.ptr.prototype.Position = function() {
		var j, obj;
		j = $clone(this, JQuery);
		obj = j.o.position();
		return new JQueryCoordinates.ptr($parseInt(obj.left) >> 0, $parseInt(obj.top) >> 0);
	};
	JQuery.prototype.Position = function() { return this.$val.Position(); };
	JQuery.ptr.prototype.ScrollLeft = function() {
		var j;
		j = $clone(this, JQuery);
		return $parseInt(j.o.scrollLeft()) >> 0;
	};
	JQuery.prototype.ScrollLeft = function() { return this.$val.ScrollLeft(); };
	JQuery.ptr.prototype.SetScrollLeft = function(value) {
		var j, value;
		j = $clone(this, JQuery);
		j.o = j.o.scrollLeft(value);
		return j;
	};
	JQuery.prototype.SetScrollLeft = function(value) { return this.$val.SetScrollLeft(value); };
	JQuery.ptr.prototype.ScrollTop = function() {
		var j;
		j = $clone(this, JQuery);
		return $parseInt(j.o.scrollTop()) >> 0;
	};
	JQuery.prototype.ScrollTop = function() { return this.$val.ScrollTop(); };
	JQuery.ptr.prototype.SetScrollTop = function(value) {
		var j, value;
		j = $clone(this, JQuery);
		j.o = j.o.scrollTop(value);
		return j;
	};
	JQuery.prototype.SetScrollTop = function(value) { return this.$val.SetScrollTop(value); };
	JQuery.ptr.prototype.ClearQueue = function(queueName) {
		var j, queueName;
		j = $clone(this, JQuery);
		j.o = j.o.clearQueue($externalize(queueName, $String));
		return j;
	};
	JQuery.prototype.ClearQueue = function(queueName) { return this.$val.ClearQueue(queueName); };
	JQuery.ptr.prototype.SetData = function(key, value) {
		var j, key, value;
		j = $clone(this, JQuery);
		j.o = j.o.data($externalize(key, $String), $externalize(value, $emptyInterface));
		return j;
	};
	JQuery.prototype.SetData = function(key, value) { return this.$val.SetData(key, value); };
	JQuery.ptr.prototype.Data = function(key) {
		var j, key, result;
		j = $clone(this, JQuery);
		result = j.o.data($externalize(key, $String));
		if (result === undefined) {
			return $ifaceNil;
		}
		return $internalize(result, $emptyInterface);
	};
	JQuery.prototype.Data = function(key) { return this.$val.Data(key); };
	JQuery.ptr.prototype.Dequeue = function(queueName) {
		var j, queueName;
		j = $clone(this, JQuery);
		j.o = j.o.dequeue($externalize(queueName, $String));
		return j;
	};
	JQuery.prototype.Dequeue = function(queueName) { return this.$val.Dequeue(queueName); };
	JQuery.ptr.prototype.RemoveData = function(name) {
		var j, name;
		j = $clone(this, JQuery);
		j.o = j.o.removeData($externalize(name, $String));
		return j;
	};
	JQuery.prototype.RemoveData = function(name) { return this.$val.RemoveData(name); };
	JQuery.ptr.prototype.OffsetParent = function() {
		var j;
		j = $clone(this, JQuery);
		j.o = j.o.offsetParent();
		return j;
	};
	JQuery.prototype.OffsetParent = function() { return this.$val.OffsetParent(); };
	JQuery.ptr.prototype.Parent = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.parent.apply(obj, $externalize(i, sliceType$26)));
		return j;
	};
	JQuery.prototype.Parent = function(i) { return this.$val.Parent(i); };
	JQuery.ptr.prototype.Parents = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.parents.apply(obj, $externalize(i, sliceType$27)));
		return j;
	};
	JQuery.prototype.Parents = function(i) { return this.$val.Parents(i); };
	JQuery.ptr.prototype.ParentsUntil = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.parentsUntil.apply(obj, $externalize(i, sliceType$28)));
		return j;
	};
	JQuery.prototype.ParentsUntil = function(i) { return this.$val.ParentsUntil(i); };
	JQuery.ptr.prototype.Prev = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.prev.apply(obj, $externalize(i, sliceType$29)));
		return j;
	};
	JQuery.prototype.Prev = function(i) { return this.$val.Prev(i); };
	JQuery.ptr.prototype.PrevAll = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.prevAll.apply(obj, $externalize(i, sliceType$30)));
		return j;
	};
	JQuery.prototype.PrevAll = function(i) { return this.$val.PrevAll(i); };
	JQuery.ptr.prototype.PrevUntil = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.prevUntil.apply(obj, $externalize(i, sliceType$31)));
		return j;
	};
	JQuery.prototype.PrevUntil = function(i) { return this.$val.PrevUntil(i); };
	JQuery.ptr.prototype.Siblings = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.siblings.apply(obj, $externalize(i, sliceType$32)));
		return j;
	};
	JQuery.prototype.Siblings = function(i) { return this.$val.Siblings(i); };
	JQuery.ptr.prototype.Slice = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.slice.apply(obj, $externalize(i, sliceType$33)));
		return j;
	};
	JQuery.prototype.Slice = function(i) { return this.$val.Slice(i); };
	JQuery.ptr.prototype.Children = function(selector) {
		var j, selector;
		j = $clone(this, JQuery);
		j.o = j.o.children($externalize(selector, $emptyInterface));
		return j;
	};
	JQuery.prototype.Children = function(selector) { return this.$val.Children(selector); };
	JQuery.ptr.prototype.Unwrap = function() {
		var j;
		j = $clone(this, JQuery);
		j.o = j.o.unwrap();
		return j;
	};
	JQuery.prototype.Unwrap = function() { return this.$val.Unwrap(); };
	JQuery.ptr.prototype.Wrap = function(obj) {
		var j, obj;
		j = $clone(this, JQuery);
		j.o = j.o.wrap($externalize(obj, $emptyInterface));
		return j;
	};
	JQuery.prototype.Wrap = function(obj) { return this.$val.Wrap(obj); };
	JQuery.ptr.prototype.WrapAll = function(i) {
		var i, j;
		j = $clone(this, JQuery);
		j.o = j.o.wrapAll($externalize(i, $emptyInterface));
		return j;
	};
	JQuery.prototype.WrapAll = function(i) { return this.$val.WrapAll(i); };
	JQuery.ptr.prototype.WrapInner = function(i) {
		var i, j;
		j = $clone(this, JQuery);
		j.o = j.o.wrapInner($externalize(i, $emptyInterface));
		return j;
	};
	JQuery.prototype.WrapInner = function(i) { return this.$val.WrapInner(i); };
	JQuery.ptr.prototype.Next = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.next.apply(obj, $externalize(i, sliceType$34)));
		return j;
	};
	JQuery.prototype.Next = function(i) { return this.$val.Next(i); };
	JQuery.ptr.prototype.NextAll = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.nextAll.apply(obj, $externalize(i, sliceType$35)));
		return j;
	};
	JQuery.prototype.NextAll = function(i) { return this.$val.NextAll(i); };
	JQuery.ptr.prototype.NextUntil = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.nextUntil.apply(obj, $externalize(i, sliceType$36)));
		return j;
	};
	JQuery.prototype.NextUntil = function(i) { return this.$val.NextUntil(i); };
	JQuery.ptr.prototype.Not = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.not.apply(obj, $externalize(i, sliceType$37)));
		return j;
	};
	JQuery.prototype.Not = function(i) { return this.$val.Not(i); };
	JQuery.ptr.prototype.Filter = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.filter.apply(obj, $externalize(i, sliceType$38)));
		return j;
	};
	JQuery.prototype.Filter = function(i) { return this.$val.Filter(i); };
	JQuery.ptr.prototype.Find = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.find.apply(obj, $externalize(i, sliceType$39)));
		return j;
	};
	JQuery.prototype.Find = function(i) { return this.$val.Find(i); };
	JQuery.ptr.prototype.First = function() {
		var j;
		j = $clone(this, JQuery);
		j.o = j.o.first();
		return j;
	};
	JQuery.prototype.First = function() { return this.$val.First(); };
	JQuery.ptr.prototype.Has = function(selector) {
		var j, selector;
		j = $clone(this, JQuery);
		j.o = j.o.has($externalize(selector, $String));
		return j;
	};
	JQuery.prototype.Has = function(selector) { return this.$val.Has(selector); };
	JQuery.ptr.prototype.Is = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		return !!((obj = j.o, obj.is.apply(obj, $externalize(i, sliceType$40))));
	};
	JQuery.prototype.Is = function(i) { return this.$val.Is(i); };
	JQuery.ptr.prototype.Last = function() {
		var j;
		j = $clone(this, JQuery);
		j.o = j.o.last();
		return j;
	};
	JQuery.prototype.Last = function() { return this.$val.Last(); };
	JQuery.ptr.prototype.Ready = function(handler) {
		var handler, j;
		j = $clone(this, JQuery);
		j.o = j.o.ready($externalize(handler, funcType$6));
		return j;
	};
	JQuery.prototype.Ready = function(handler) { return this.$val.Ready(handler); };
	JQuery.ptr.prototype.Resize = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.resize.apply(obj, $externalize(i, sliceType$41)));
		return j;
	};
	JQuery.prototype.Resize = function(i) { return this.$val.Resize(i); };
	JQuery.ptr.prototype.Scroll = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.scroll.apply(obj, $externalize(i, sliceType$42)));
		return j;
	};
	JQuery.prototype.Scroll = function(i) { return this.$val.Scroll(i); };
	JQuery.ptr.prototype.FadeOut = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.fadeOut.apply(obj, $externalize(i, sliceType$43)));
		return j;
	};
	JQuery.prototype.FadeOut = function(i) { return this.$val.FadeOut(i); };
	JQuery.ptr.prototype.Select = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.select.apply(obj, $externalize(i, sliceType$44)));
		return j;
	};
	JQuery.prototype.Select = function(i) { return this.$val.Select(i); };
	JQuery.ptr.prototype.Submit = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.submit.apply(obj, $externalize(i, sliceType$45)));
		return j;
	};
	JQuery.prototype.Submit = function(i) { return this.$val.Submit(i); };
	JQuery.ptr.prototype.Trigger = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.trigger.apply(obj, $externalize(i, sliceType$46)));
		return j;
	};
	JQuery.prototype.Trigger = function(i) { return this.$val.Trigger(i); };
	JQuery.ptr.prototype.On = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.on.apply(obj, $externalize(i, sliceType$47)));
		return j;
	};
	JQuery.prototype.On = function(i) { return this.$val.On(i); };
	JQuery.ptr.prototype.One = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.one.apply(obj, $externalize(i, sliceType$48)));
		return j;
	};
	JQuery.prototype.One = function(i) { return this.$val.One(i); };
	JQuery.ptr.prototype.Off = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.off.apply(obj, $externalize(i, sliceType$49)));
		return j;
	};
	JQuery.prototype.Off = function(i) { return this.$val.Off(i); };
	JQuery.ptr.prototype.Load = function(i) {
		var i, j, obj;
		j = $clone(this, JQuery);
		j.o = (obj = j.o, obj.load.apply(obj, $externalize(i, sliceType$50)));
		return j;
	};
	JQuery.prototype.Load = function(i) { return this.$val.Load(i); };
	JQuery.ptr.prototype.Serialize = function() {
		var j;
		j = $clone(this, JQuery);
		return $internalize(j.o.serialize(), $String);
	};
	JQuery.prototype.Serialize = function() { return this.$val.Serialize(); };
	JQuery.ptr.prototype.SerializeArray = function() {
		var j;
		j = $clone(this, JQuery);
		return j.o.serializeArray();
	};
	JQuery.prototype.SerializeArray = function() { return this.$val.SerializeArray(); };
	Ajax = $pkg.Ajax = function(options) {
		var options;
		return new Deferred.ptr($global.jQuery.ajax($externalize(options, mapType$2)));
	};
	AjaxPrefilter = $pkg.AjaxPrefilter = function(i) {
		var i, obj;
		(obj = $global.jQuery, obj.ajaxPrefilter.apply(obj, $externalize(i, sliceType$51)));
	};
	AjaxSetup = $pkg.AjaxSetup = function(options) {
		var options;
		$global.jQuery.ajaxSetup($externalize(options, mapType$3));
	};
	AjaxTransport = $pkg.AjaxTransport = function(i) {
		var i, obj;
		(obj = $global.jQuery, obj.ajaxTransport.apply(obj, $externalize(i, sliceType$52)));
	};
	Get = $pkg.Get = function(i) {
		var i, obj;
		return new Deferred.ptr((obj = $global.jQuery, obj.get.apply(obj, $externalize(i, sliceType$53))));
	};
	Post = $pkg.Post = function(i) {
		var i, obj;
		return new Deferred.ptr((obj = $global.jQuery, obj.post.apply(obj, $externalize(i, sliceType$54))));
	};
	GetJSON = $pkg.GetJSON = function(i) {
		var i, obj;
		return new Deferred.ptr((obj = $global.jQuery, obj.getJSON.apply(obj, $externalize(i, sliceType$55))));
	};
	GetScript = $pkg.GetScript = function(i) {
		var i, obj;
		return new Deferred.ptr((obj = $global.jQuery, obj.getScript.apply(obj, $externalize(i, sliceType$56))));
	};
	Deferred.ptr.prototype.Promise = function() {
		var d;
		d = $clone(this, Deferred);
		return d.Object.promise();
	};
	Deferred.prototype.Promise = function() { return this.$val.Promise(); };
	Deferred.ptr.prototype.Then = function(fn) {
		var d, fn, obj;
		d = $clone(this, Deferred);
		return new Deferred.ptr((obj = d.Object, obj.then.apply(obj, $externalize(fn, sliceType$57))));
	};
	Deferred.prototype.Then = function(fn) { return this.$val.Then(fn); };
	Deferred.ptr.prototype.Always = function(fn) {
		var d, fn, obj;
		d = $clone(this, Deferred);
		return new Deferred.ptr((obj = d.Object, obj.always.apply(obj, $externalize(fn, sliceType$58))));
	};
	Deferred.prototype.Always = function(fn) { return this.$val.Always(fn); };
	Deferred.ptr.prototype.Done = function(fn) {
		var d, fn, obj;
		d = $clone(this, Deferred);
		return new Deferred.ptr((obj = d.Object, obj.done.apply(obj, $externalize(fn, sliceType$59))));
	};
	Deferred.prototype.Done = function(fn) { return this.$val.Done(fn); };
	Deferred.ptr.prototype.Fail = function(fn) {
		var d, fn, obj;
		d = $clone(this, Deferred);
		return new Deferred.ptr((obj = d.Object, obj.fail.apply(obj, $externalize(fn, sliceType$60))));
	};
	Deferred.prototype.Fail = function(fn) { return this.$val.Fail(fn); };
	Deferred.ptr.prototype.Progress = function(fn) {
		var d, fn;
		d = $clone(this, Deferred);
		return new Deferred.ptr(d.Object.progress($externalize(fn, $emptyInterface)));
	};
	Deferred.prototype.Progress = function(fn) { return this.$val.Progress(fn); };
	When = $pkg.When = function(d) {
		var d, obj;
		return new Deferred.ptr((obj = $global.jQuery, obj.when.apply(obj, $externalize(d, sliceType$61))));
	};
	Deferred.ptr.prototype.State = function() {
		var d;
		d = $clone(this, Deferred);
		return $internalize(d.Object.state(), $String);
	};
	Deferred.prototype.State = function() { return this.$val.State(); };
	NewDeferred = $pkg.NewDeferred = function() {
		return new Deferred.ptr($global.jQuery.Deferred());
	};
	Deferred.ptr.prototype.Resolve = function(i) {
		var d, i, obj;
		d = $clone(this, Deferred);
		return new Deferred.ptr((obj = d.Object, obj.resolve.apply(obj, $externalize(i, sliceType$62))));
	};
	Deferred.prototype.Resolve = function(i) { return this.$val.Resolve(i); };
	Deferred.ptr.prototype.Reject = function(i) {
		var d, i, obj;
		d = $clone(this, Deferred);
		return new Deferred.ptr((obj = d.Object, obj.reject.apply(obj, $externalize(i, sliceType$63))));
	};
	Deferred.prototype.Reject = function(i) { return this.$val.Reject(i); };
	Deferred.ptr.prototype.Notify = function(i) {
		var d, i;
		d = $clone(this, Deferred);
		return new Deferred.ptr(d.Object.notify($externalize(i, $emptyInterface)));
	};
	Deferred.prototype.Notify = function(i) { return this.$val.Notify(i); };
	JQuery.methods = [{prop: "Each", name: "Each", pkg: "", typ: $funcType([funcType$1], [JQuery], false)}, {prop: "Underlying", name: "Underlying", pkg: "", typ: $funcType([], [ptrType], false)}, {prop: "Get", name: "Get", pkg: "", typ: $funcType([sliceType$6], [ptrType$1], true)}, {prop: "Append", name: "Append", pkg: "", typ: $funcType([sliceType$7], [JQuery], true)}, {prop: "Empty", name: "Empty", pkg: "", typ: $funcType([], [JQuery], false)}, {prop: "Detach", name: "Detach", pkg: "", typ: $funcType([sliceType$8], [JQuery], true)}, {prop: "Eq", name: "Eq", pkg: "", typ: $funcType([$Int], [JQuery], false)}, {prop: "FadeIn", name: "FadeIn", pkg: "", typ: $funcType([sliceType$9], [JQuery], true)}, {prop: "Delay", name: "Delay", pkg: "", typ: $funcType([sliceType$10], [JQuery], true)}, {prop: "ToArray", name: "ToArray", pkg: "", typ: $funcType([], [sliceType$64], false)}, {prop: "Remove", name: "Remove", pkg: "", typ: $funcType([sliceType$12], [JQuery], true)}, {prop: "Stop", name: "Stop", pkg: "", typ: $funcType([sliceType$13], [JQuery], true)}, {prop: "AddBack", name: "AddBack", pkg: "", typ: $funcType([sliceType$14], [JQuery], true)}, {prop: "Css", name: "Css", pkg: "", typ: $funcType([$String], [$String], false)}, {prop: "CssArray", name: "CssArray", pkg: "", typ: $funcType([sliceType$15], [mapType$4], true)}, {prop: "SetCss", name: "SetCss", pkg: "", typ: $funcType([sliceType$16], [JQuery], true)}, {prop: "Text", name: "Text", pkg: "", typ: $funcType([], [$String], false)}, {prop: "SetText", name: "SetText", pkg: "", typ: $funcType([$emptyInterface], [JQuery], false)}, {prop: "Val", name: "Val", pkg: "", typ: $funcType([], [$String], false)}, {prop: "SetVal", name: "SetVal", pkg: "", typ: $funcType([$emptyInterface], [JQuery], false)}, {prop: "Prop", name: "Prop", pkg: "", typ: $funcType([$String], [$emptyInterface], false)}, {prop: "SetProp", name: "SetProp", pkg: "", typ: $funcType([sliceType$17], [JQuery], true)}, {prop: "RemoveProp", name: "RemoveProp", pkg: "", typ: $funcType([$String], [JQuery], false)}, {prop: "Attr", name: "Attr", pkg: "", typ: $funcType([$String], [$String], false)}, {prop: "SetAttr", name: "SetAttr", pkg: "", typ: $funcType([sliceType$18], [JQuery], true)}, {prop: "RemoveAttr", name: "RemoveAttr", pkg: "", typ: $funcType([$String], [JQuery], false)}, {prop: "HasClass", name: "HasClass", pkg: "", typ: $funcType([$String], [$Bool], false)}, {prop: "AddClass", name: "AddClass", pkg: "", typ: $funcType([$emptyInterface], [JQuery], false)}, {prop: "RemoveClass", name: "RemoveClass", pkg: "", typ: $funcType([$String], [JQuery], false)}, {prop: "ToggleClass", name: "ToggleClass", pkg: "", typ: $funcType([sliceType$19], [JQuery], true)}, {prop: "Focus", name: "Focus", pkg: "", typ: $funcType([], [JQuery], false)}, {prop: "Blur", name: "Blur", pkg: "", typ: $funcType([], [JQuery], false)}, {prop: "ReplaceAll", name: "ReplaceAll", pkg: "", typ: $funcType([$emptyInterface], [JQuery], false)}, {prop: "ReplaceWith", name: "ReplaceWith", pkg: "", typ: $funcType([$emptyInterface], [JQuery], false)}, {prop: "After", name: "After", pkg: "", typ: $funcType([sliceType$20], [JQuery], true)}, {prop: "Before", name: "Before", pkg: "", typ: $funcType([sliceType$21], [JQuery], true)}, {prop: "Prepend", name: "Prepend", pkg: "", typ: $funcType([sliceType$22], [JQuery], true)}, {prop: "PrependTo", name: "PrependTo", pkg: "", typ: $funcType([$emptyInterface], [JQuery], false)}, {prop: "AppendTo", name: "AppendTo", pkg: "", typ: $funcType([$emptyInterface], [JQuery], false)}, {prop: "InsertAfter", name: "InsertAfter", pkg: "", typ: $funcType([$emptyInterface], [JQuery], false)}, {prop: "InsertBefore", name: "InsertBefore", pkg: "", typ: $funcType([$emptyInterface], [JQuery], false)}, {prop: "Show", name: "Show", pkg: "", typ: $funcType([], [JQuery], false)}, {prop: "Hide", name: "Hide", pkg: "", typ: $funcType([], [JQuery], false)}, {prop: "Toggle", name: "Toggle", pkg: "", typ: $funcType([$Bool], [JQuery], false)}, {prop: "Contents", name: "Contents", pkg: "", typ: $funcType([], [JQuery], false)}, {prop: "Html", name: "Html", pkg: "", typ: $funcType([], [$String], false)}, {prop: "SetHtml", name: "SetHtml", pkg: "", typ: $funcType([$emptyInterface], [JQuery], false)}, {prop: "Closest", name: "Closest", pkg: "", typ: $funcType([sliceType$23], [JQuery], true)}, {prop: "End", name: "End", pkg: "", typ: $funcType([], [JQuery], false)}, {prop: "Add", name: "Add", pkg: "", typ: $funcType([sliceType$24], [JQuery], true)}, {prop: "Clone", name: "Clone", pkg: "", typ: $funcType([sliceType$25], [JQuery], true)}, {prop: "Height", name: "Height", pkg: "", typ: $funcType([], [$Int], false)}, {prop: "SetHeight", name: "SetHeight", pkg: "", typ: $funcType([$String], [JQuery], false)}, {prop: "Width", name: "Width", pkg: "", typ: $funcType([], [$Int], false)}, {prop: "SetWidth", name: "SetWidth", pkg: "", typ: $funcType([$emptyInterface], [JQuery], false)}, {prop: "InnerHeight", name: "InnerHeight", pkg: "", typ: $funcType([], [$Int], false)}, {prop: "InnerWidth", name: "InnerWidth", pkg: "", typ: $funcType([], [$Int], false)}, {prop: "Offset", name: "Offset", pkg: "", typ: $funcType([], [JQueryCoordinates], false)}, {prop: "SetOffset", name: "SetOffset", pkg: "", typ: $funcType([JQueryCoordinates], [JQuery], false)}, {prop: "OuterHeight", name: "OuterHeight", pkg: "", typ: $funcType([sliceType$65], [$Int], true)}, {prop: "OuterWidth", name: "OuterWidth", pkg: "", typ: $funcType([sliceType$66], [$Int], true)}, {prop: "Position", name: "Position", pkg: "", typ: $funcType([], [JQueryCoordinates], false)}, {prop: "ScrollLeft", name: "ScrollLeft", pkg: "", typ: $funcType([], [$Int], false)}, {prop: "SetScrollLeft", name: "SetScrollLeft", pkg: "", typ: $funcType([$Int], [JQuery], false)}, {prop: "ScrollTop", name: "ScrollTop", pkg: "", typ: $funcType([], [$Int], false)}, {prop: "SetScrollTop", name: "SetScrollTop", pkg: "", typ: $funcType([$Int], [JQuery], false)}, {prop: "ClearQueue", name: "ClearQueue", pkg: "", typ: $funcType([$String], [JQuery], false)}, {prop: "SetData", name: "SetData", pkg: "", typ: $funcType([$String, $emptyInterface], [JQuery], false)}, {prop: "Data", name: "Data", pkg: "", typ: $funcType([$String], [$emptyInterface], false)}, {prop: "Dequeue", name: "Dequeue", pkg: "", typ: $funcType([$String], [JQuery], false)}, {prop: "RemoveData", name: "RemoveData", pkg: "", typ: $funcType([$String], [JQuery], false)}, {prop: "OffsetParent", name: "OffsetParent", pkg: "", typ: $funcType([], [JQuery], false)}, {prop: "Parent", name: "Parent", pkg: "", typ: $funcType([sliceType$26], [JQuery], true)}, {prop: "Parents", name: "Parents", pkg: "", typ: $funcType([sliceType$27], [JQuery], true)}, {prop: "ParentsUntil", name: "ParentsUntil", pkg: "", typ: $funcType([sliceType$28], [JQuery], true)}, {prop: "Prev", name: "Prev", pkg: "", typ: $funcType([sliceType$29], [JQuery], true)}, {prop: "PrevAll", name: "PrevAll", pkg: "", typ: $funcType([sliceType$30], [JQuery], true)}, {prop: "PrevUntil", name: "PrevUntil", pkg: "", typ: $funcType([sliceType$31], [JQuery], true)}, {prop: "Siblings", name: "Siblings", pkg: "", typ: $funcType([sliceType$32], [JQuery], true)}, {prop: "Slice", name: "Slice", pkg: "", typ: $funcType([sliceType$33], [JQuery], true)}, {prop: "Children", name: "Children", pkg: "", typ: $funcType([$emptyInterface], [JQuery], false)}, {prop: "Unwrap", name: "Unwrap", pkg: "", typ: $funcType([], [JQuery], false)}, {prop: "Wrap", name: "Wrap", pkg: "", typ: $funcType([$emptyInterface], [JQuery], false)}, {prop: "WrapAll", name: "WrapAll", pkg: "", typ: $funcType([$emptyInterface], [JQuery], false)}, {prop: "WrapInner", name: "WrapInner", pkg: "", typ: $funcType([$emptyInterface], [JQuery], false)}, {prop: "Next", name: "Next", pkg: "", typ: $funcType([sliceType$34], [JQuery], true)}, {prop: "NextAll", name: "NextAll", pkg: "", typ: $funcType([sliceType$35], [JQuery], true)}, {prop: "NextUntil", name: "NextUntil", pkg: "", typ: $funcType([sliceType$36], [JQuery], true)}, {prop: "Not", name: "Not", pkg: "", typ: $funcType([sliceType$37], [JQuery], true)}, {prop: "Filter", name: "Filter", pkg: "", typ: $funcType([sliceType$38], [JQuery], true)}, {prop: "Find", name: "Find", pkg: "", typ: $funcType([sliceType$39], [JQuery], true)}, {prop: "First", name: "First", pkg: "", typ: $funcType([], [JQuery], false)}, {prop: "Has", name: "Has", pkg: "", typ: $funcType([$String], [JQuery], false)}, {prop: "Is", name: "Is", pkg: "", typ: $funcType([sliceType$40], [$Bool], true)}, {prop: "Last", name: "Last", pkg: "", typ: $funcType([], [JQuery], false)}, {prop: "Ready", name: "Ready", pkg: "", typ: $funcType([funcType$6], [JQuery], false)}, {prop: "Resize", name: "Resize", pkg: "", typ: $funcType([sliceType$41], [JQuery], true)}, {prop: "Scroll", name: "Scroll", pkg: "", typ: $funcType([sliceType$42], [JQuery], true)}, {prop: "FadeOut", name: "FadeOut", pkg: "", typ: $funcType([sliceType$43], [JQuery], true)}, {prop: "Select", name: "Select", pkg: "", typ: $funcType([sliceType$44], [JQuery], true)}, {prop: "Submit", name: "Submit", pkg: "", typ: $funcType([sliceType$45], [JQuery], true)}, {prop: "Trigger", name: "Trigger", pkg: "", typ: $funcType([sliceType$46], [JQuery], true)}, {prop: "On", name: "On", pkg: "", typ: $funcType([sliceType$47], [JQuery], true)}, {prop: "One", name: "One", pkg: "", typ: $funcType([sliceType$48], [JQuery], true)}, {prop: "Off", name: "Off", pkg: "", typ: $funcType([sliceType$49], [JQuery], true)}, {prop: "Load", name: "Load", pkg: "", typ: $funcType([sliceType$50], [JQuery], true)}, {prop: "Serialize", name: "Serialize", pkg: "", typ: $funcType([], [$String], false)}, {prop: "SerializeArray", name: "SerializeArray", pkg: "", typ: $funcType([], [ptrType$2], false)}];
	ptrType$4.methods = [{prop: "PreventDefault", name: "PreventDefault", pkg: "", typ: $funcType([], [], false)}, {prop: "IsDefaultPrevented", name: "IsDefaultPrevented", pkg: "", typ: $funcType([], [$Bool], false)}, {prop: "IsImmediatePropogationStopped", name: "IsImmediatePropogationStopped", pkg: "", typ: $funcType([], [$Bool], false)}, {prop: "IsPropagationStopped", name: "IsPropagationStopped", pkg: "", typ: $funcType([], [$Bool], false)}, {prop: "StopImmediatePropagation", name: "StopImmediatePropagation", pkg: "", typ: $funcType([], [], false)}, {prop: "StopPropagation", name: "StopPropagation", pkg: "", typ: $funcType([], [], false)}];
	Deferred.methods = [{prop: "Promise", name: "Promise", pkg: "", typ: $funcType([], [ptrType$12], false)}, {prop: "Then", name: "Then", pkg: "", typ: $funcType([sliceType$57], [Deferred], true)}, {prop: "Always", name: "Always", pkg: "", typ: $funcType([sliceType$58], [Deferred], true)}, {prop: "Done", name: "Done", pkg: "", typ: $funcType([sliceType$59], [Deferred], true)}, {prop: "Fail", name: "Fail", pkg: "", typ: $funcType([sliceType$60], [Deferred], true)}, {prop: "Progress", name: "Progress", pkg: "", typ: $funcType([$emptyInterface], [Deferred], false)}, {prop: "State", name: "State", pkg: "", typ: $funcType([], [$String], false)}, {prop: "Resolve", name: "Resolve", pkg: "", typ: $funcType([sliceType$62], [Deferred], true)}, {prop: "Reject", name: "Reject", pkg: "", typ: $funcType([sliceType$63], [Deferred], true)}, {prop: "Notify", name: "Notify", pkg: "", typ: $funcType([$emptyInterface], [Deferred], false)}];
	JQuery.init([{prop: "o", name: "o", pkg: "github.com/albrow/jquery", typ: ptrType$3, tag: ""}, {prop: "Jquery", name: "Jquery", pkg: "", typ: $String, tag: "js:\"jquery\""}, {prop: "Selector", name: "Selector", pkg: "", typ: $String, tag: "js:\"selector\""}, {prop: "Length", name: "Length", pkg: "", typ: $Int, tag: "js:\"length\""}, {prop: "Context", name: "Context", pkg: "", typ: $String, tag: "js:\"context\""}]);
	Event.init([{prop: "Object", name: "", pkg: "", typ: ptrType$5, tag: ""}, {prop: "KeyCode", name: "KeyCode", pkg: "", typ: $Int, tag: "js:\"keyCode\""}, {prop: "Target", name: "Target", pkg: "", typ: ptrType$6, tag: "js:\"target\""}, {prop: "CurrentTarget", name: "CurrentTarget", pkg: "", typ: ptrType$7, tag: "js:\"currentTarget\""}, {prop: "DelegateTarget", name: "DelegateTarget", pkg: "", typ: ptrType$8, tag: "js:\"delegateTarget\""}, {prop: "RelatedTarget", name: "RelatedTarget", pkg: "", typ: ptrType$9, tag: "js:\"relatedTarget\""}, {prop: "Data", name: "Data", pkg: "", typ: ptrType$10, tag: "js:\"data\""}, {prop: "Result", name: "Result", pkg: "", typ: ptrType$11, tag: "js:\"result\""}, {prop: "Which", name: "Which", pkg: "", typ: $Int, tag: "js:\"which\""}, {prop: "Namespace", name: "Namespace", pkg: "", typ: $String, tag: "js:\"namespace\""}, {prop: "MetaKey", name: "MetaKey", pkg: "", typ: $Bool, tag: "js:\"metaKey\""}, {prop: "PageX", name: "PageX", pkg: "", typ: $Int, tag: "js:\"pageX\""}, {prop: "PageY", name: "PageY", pkg: "", typ: $Int, tag: "js:\"pageY\""}, {prop: "Type", name: "Type", pkg: "", typ: $String, tag: "js:\"type\""}]);
	JQueryCoordinates.init([{prop: "Left", name: "Left", pkg: "", typ: $Int, tag: ""}, {prop: "Top", name: "Top", pkg: "", typ: $Int, tag: ""}]);
	Deferred.init([{prop: "Object", name: "", pkg: "", typ: ptrType$13, tag: ""}]);
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_jquery = function() { while (true) { switch ($s) { case 0:
		$r = js.$init($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		/* */ } return; } }; $init_jquery.$blocking = true; return $init_jquery;
	};
	return $pkg;
})();
$packages["github.com/rusco/qunit"] = (function() {
	var $pkg = {}, js, QUnitAssert, sliceType, funcType, funcType$1, ptrType, funcType$2, funcType$12, funcType$13, funcType$14, ptrType$7, ptrType$8, ptrType$9, log, Test, Ok, Start, AsyncTest, Expect, Module, ModuleLifecycle;
	js = $packages["github.com/gopherjs/gopherjs/js"];
	QUnitAssert = $pkg.QUnitAssert = $newType(0, $kindStruct, "qunit.QUnitAssert", "QUnitAssert", "github.com/rusco/qunit", function(Object_) {
		this.$val = this;
		this.Object = Object_ !== undefined ? Object_ : null;
	});
	sliceType = $sliceType($emptyInterface);
	funcType = $funcType([], [$emptyInterface], false);
	funcType$1 = $funcType([], [$emptyInterface], false);
	ptrType = $ptrType(js.Object);
	funcType$2 = $funcType([ptrType], [], false);
	funcType$12 = $funcType([], [], false);
	funcType$13 = $funcType([], [], false);
	funcType$14 = $funcType([], [], false);
	ptrType$7 = $ptrType(js.Object);
	ptrType$8 = $ptrType(js.Object);
	ptrType$9 = $ptrType(js.Object);
	log = function(i) {
		var i, obj;
		(obj = $global.console, obj.log.apply(obj, $externalize(i, sliceType)));
	};
	QUnitAssert.ptr.prototype.DeepEqual = function(actual, expected, message) {
		var actual, expected, message, qa;
		qa = $clone(this, QUnitAssert);
		return !!(qa.Object.deepEqual($externalize(actual, $emptyInterface), $externalize(expected, $emptyInterface), $externalize(message, $String)));
	};
	QUnitAssert.prototype.DeepEqual = function(actual, expected, message) { return this.$val.DeepEqual(actual, expected, message); };
	QUnitAssert.ptr.prototype.Equal = function(actual, expected, message) {
		var actual, expected, message, qa;
		qa = $clone(this, QUnitAssert);
		log(new sliceType([new $String("---> qunit: "), actual, expected, new $jsObjectPtr(qa.Object.equal($externalize(actual, $emptyInterface), $externalize(expected, $emptyInterface), $externalize(message, $String))), new $Bool(!!(qa.Object.equal($externalize(actual, $emptyInterface), $externalize(expected, $emptyInterface), $externalize(message, $String))))]));
		return !!(qa.Object.equal($externalize(actual, $emptyInterface), $externalize(expected, $emptyInterface), $externalize(message, $String)));
	};
	QUnitAssert.prototype.Equal = function(actual, expected, message) { return this.$val.Equal(actual, expected, message); };
	QUnitAssert.ptr.prototype.NotDeepEqual = function(actual, expected, message) {
		var actual, expected, message, qa;
		qa = $clone(this, QUnitAssert);
		return !!(qa.Object.notDeepEqual($externalize(actual, $emptyInterface), $externalize(expected, $emptyInterface), $externalize(message, $String)));
	};
	QUnitAssert.prototype.NotDeepEqual = function(actual, expected, message) { return this.$val.NotDeepEqual(actual, expected, message); };
	QUnitAssert.ptr.prototype.NotEqual = function(actual, expected, message) {
		var actual, expected, message, qa;
		qa = $clone(this, QUnitAssert);
		return !!(qa.Object.notEqual($externalize(actual, $emptyInterface), $externalize(expected, $emptyInterface), $externalize(message, $String)));
	};
	QUnitAssert.prototype.NotEqual = function(actual, expected, message) { return this.$val.NotEqual(actual, expected, message); };
	QUnitAssert.ptr.prototype.NotPropEqual = function(actual, expected, message) {
		var actual, expected, message, qa;
		qa = $clone(this, QUnitAssert);
		return !!(qa.Object.notPropEqual($externalize(actual, $emptyInterface), $externalize(expected, $emptyInterface), $externalize(message, $String)));
	};
	QUnitAssert.prototype.NotPropEqual = function(actual, expected, message) { return this.$val.NotPropEqual(actual, expected, message); };
	QUnitAssert.ptr.prototype.PropEqual = function(actual, expected, message) {
		var actual, expected, message, qa;
		qa = $clone(this, QUnitAssert);
		return !!(qa.Object.propEqual($externalize(actual, $emptyInterface), $externalize(expected, $emptyInterface), $externalize(message, $String)));
	};
	QUnitAssert.prototype.PropEqual = function(actual, expected, message) { return this.$val.PropEqual(actual, expected, message); };
	QUnitAssert.ptr.prototype.NotStrictEqual = function(actual, expected, message) {
		var actual, expected, message, qa;
		qa = $clone(this, QUnitAssert);
		return !!(qa.Object.notStrictEqual($externalize(actual, $emptyInterface), $externalize(expected, $emptyInterface), $externalize(message, $String)));
	};
	QUnitAssert.prototype.NotStrictEqual = function(actual, expected, message) { return this.$val.NotStrictEqual(actual, expected, message); };
	QUnitAssert.ptr.prototype.Ok = function(state, message) {
		var message, qa, state;
		qa = $clone(this, QUnitAssert);
		return !!(qa.Object.ok($externalize(state, $emptyInterface), $externalize(message, $String)));
	};
	QUnitAssert.prototype.Ok = function(state, message) { return this.$val.Ok(state, message); };
	QUnitAssert.ptr.prototype.StrictEqual = function(actual, expected, message) {
		var actual, expected, message, qa;
		qa = $clone(this, QUnitAssert);
		return !!(qa.Object.strictEqual($externalize(actual, $emptyInterface), $externalize(expected, $emptyInterface), $externalize(message, $String)));
	};
	QUnitAssert.prototype.StrictEqual = function(actual, expected, message) { return this.$val.StrictEqual(actual, expected, message); };
	QUnitAssert.ptr.prototype.ThrowsExpected = function(block, expected, message) {
		var block, expected, message, qa;
		qa = $clone(this, QUnitAssert);
		return qa.Object.throwsExpected($externalize(block, funcType), $externalize(expected, $emptyInterface), $externalize(message, $String));
	};
	QUnitAssert.prototype.ThrowsExpected = function(block, expected, message) { return this.$val.ThrowsExpected(block, expected, message); };
	QUnitAssert.ptr.prototype.Throws = function(block, message) {
		var block, message, qa;
		qa = $clone(this, QUnitAssert);
		return qa.Object.throws($externalize(block, funcType$1), $externalize(message, $String));
	};
	QUnitAssert.prototype.Throws = function(block, message) { return this.$val.Throws(block, message); };
	Test = $pkg.Test = function(name, testFn) {
		var name, testFn;
		$global.QUnit.test($externalize(name, $String), $externalize((function(e) {
			var e;
			testFn(new QUnitAssert.ptr(e));
		}), funcType$2));
	};
	Ok = $pkg.Ok = function(state, message) {
		var message, state;
		return $global.QUnit.ok($externalize(state, $emptyInterface), $externalize(message, $String));
	};
	Start = $pkg.Start = function() {
		return $global.QUnit.start();
	};
	AsyncTest = $pkg.AsyncTest = function(name, testFn) {
		var name, t, testFn;
		t = $global.QUnit.asyncTest($externalize(name, $String), $externalize((function() {
			testFn();
		}), funcType$12));
		return t;
	};
	Expect = $pkg.Expect = function(amount) {
		var amount;
		return $global.QUnit.expect(amount);
	};
	Module = $pkg.Module = function(name) {
		var name;
		return $global.QUnit.module($externalize(name, $String));
	};
	ModuleLifecycle = $pkg.ModuleLifecycle = function(name, lc) {
		var lc, name, o;
		o = new ($global.Object)();
		if (!($methodVal(lc, "Setup") === $throwNilPointerError)) {
			o.setup = $externalize($methodVal(lc, "Setup"), funcType$13);
		}
		if (!($methodVal(lc, "Teardown") === $throwNilPointerError)) {
			o.teardown = $externalize($methodVal(lc, "Teardown"), funcType$14);
		}
		return $global.QUnit.module($externalize(name, $String), o);
	};
	QUnitAssert.methods = [{prop: "DeepEqual", name: "DeepEqual", pkg: "", typ: $funcType([$emptyInterface, $emptyInterface, $String], [$Bool], false)}, {prop: "Equal", name: "Equal", pkg: "", typ: $funcType([$emptyInterface, $emptyInterface, $String], [$Bool], false)}, {prop: "NotDeepEqual", name: "NotDeepEqual", pkg: "", typ: $funcType([$emptyInterface, $emptyInterface, $String], [$Bool], false)}, {prop: "NotEqual", name: "NotEqual", pkg: "", typ: $funcType([$emptyInterface, $emptyInterface, $String], [$Bool], false)}, {prop: "NotPropEqual", name: "NotPropEqual", pkg: "", typ: $funcType([$emptyInterface, $emptyInterface, $String], [$Bool], false)}, {prop: "PropEqual", name: "PropEqual", pkg: "", typ: $funcType([$emptyInterface, $emptyInterface, $String], [$Bool], false)}, {prop: "NotStrictEqual", name: "NotStrictEqual", pkg: "", typ: $funcType([$emptyInterface, $emptyInterface, $String], [$Bool], false)}, {prop: "Ok", name: "Ok", pkg: "", typ: $funcType([$emptyInterface, $String], [$Bool], false)}, {prop: "StrictEqual", name: "StrictEqual", pkg: "", typ: $funcType([$emptyInterface, $emptyInterface, $String], [$Bool], false)}, {prop: "ThrowsExpected", name: "ThrowsExpected", pkg: "", typ: $funcType([funcType, $emptyInterface, $String], [ptrType$7], false)}, {prop: "Throws", name: "Throws", pkg: "", typ: $funcType([funcType$1, $String], [ptrType$8], false)}];
	QUnitAssert.init([{prop: "Object", name: "", pkg: "", typ: ptrType$9, tag: ""}]);
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_qunit = function() { while (true) { switch ($s) { case 0:
		$r = js.$init($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		/* */ } return; } }; $init_qunit.$blocking = true; return $init_qunit;
	};
	return $pkg;
})();
$packages["errors"] = (function() {
	var $pkg = {}, errorString, ptrType, New;
	errorString = $pkg.errorString = $newType(0, $kindStruct, "errors.errorString", "errorString", "errors", function(s_) {
		this.$val = this;
		this.s = s_ !== undefined ? s_ : "";
	});
	ptrType = $ptrType(errorString);
	New = $pkg.New = function(text) {
		var text;
		return new errorString.ptr(text);
	};
	errorString.ptr.prototype.Error = function() {
		var e;
		e = this;
		return e.s;
	};
	errorString.prototype.Error = function() { return this.$val.Error(); };
	ptrType.methods = [{prop: "Error", name: "Error", pkg: "", typ: $funcType([], [$String], false)}];
	errorString.init([{prop: "s", name: "s", pkg: "errors", typ: $String, tag: ""}]);
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_errors = function() { while (true) { switch ($s) { case 0:
		/* */ } return; } }; $init_errors.$blocking = true; return $init_errors;
	};
	return $pkg;
})();
$packages["math"] = (function() {
	var $pkg = {}, js, arrayType, arrayType$1, arrayType$2, structType, arrayType$3, math, buf, pow10tab, init, init$1, Float32bits, Float32frombits, init$2;
	js = $packages["github.com/gopherjs/gopherjs/js"];
	arrayType = $arrayType($Uint32, 2);
	arrayType$1 = $arrayType($Float32, 2);
	arrayType$2 = $arrayType($Float64, 1);
	structType = $structType([{prop: "uint32array", name: "uint32array", pkg: "math", typ: arrayType, tag: ""}, {prop: "float32array", name: "float32array", pkg: "math", typ: arrayType$1, tag: ""}, {prop: "float64array", name: "float64array", pkg: "math", typ: arrayType$2, tag: ""}]);
	arrayType$3 = $arrayType($Float64, 70);
	init = function() {
		Float32bits(0);
		Float32frombits(0);
	};
	init$1 = function() {
		var ab;
		ab = new ($global.ArrayBuffer)(8);
		buf.uint32array = new ($global.Uint32Array)(ab);
		buf.float32array = new ($global.Float32Array)(ab);
		buf.float64array = new ($global.Float64Array)(ab);
	};
	Float32bits = $pkg.Float32bits = function(f) {
		var f;
		buf.float32array[0] = f;
		return buf.uint32array[0];
	};
	Float32frombits = $pkg.Float32frombits = function(b) {
		var b;
		buf.uint32array[0] = b;
		return buf.float32array[0];
	};
	init$2 = function() {
		var _q, i, m, x;
		pow10tab[0] = 1;
		pow10tab[1] = 10;
		i = 2;
		while (true) {
			if (!(i < 70)) { break; }
			m = (_q = i / 2, (_q === _q && _q !== 1/0 && _q !== -1/0) ? _q >> 0 : $throwRuntimeError("integer divide by zero"));
			((i < 0 || i >= pow10tab.length) ? $throwRuntimeError("index out of range") : pow10tab[i] = ((m < 0 || m >= pow10tab.length) ? $throwRuntimeError("index out of range") : pow10tab[m]) * (x = i - m >> 0, ((x < 0 || x >= pow10tab.length) ? $throwRuntimeError("index out of range") : pow10tab[x])));
			i = i + (1) >> 0;
		}
	};
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_math = function() { while (true) { switch ($s) { case 0:
		$r = js.$init($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		buf = new structType.ptr();
		pow10tab = arrayType$3.zero();
		math = $global.Math;
		init();
		init$1();
		init$2();
		/* */ } return; } }; $init_math.$blocking = true; return $init_math;
	};
	return $pkg;
})();
$packages["unicode/utf8"] = (function() {
	var $pkg = {};
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_utf8 = function() { while (true) { switch ($s) { case 0:
		/* */ } return; } }; $init_utf8.$blocking = true; return $init_utf8;
	};
	return $pkg;
})();
$packages["strconv"] = (function() {
	var $pkg = {}, errors, math, utf8, sliceType$23, sliceType$24, arrayType$7, sliceType$25, sliceType$26, shifts, FormatInt, Itoa, formatBits;
	errors = $packages["errors"];
	math = $packages["math"];
	utf8 = $packages["unicode/utf8"];
	sliceType$23 = $sliceType($Uint8);
	sliceType$24 = $sliceType($Uint8);
	arrayType$7 = $arrayType($Uint8, 65);
	sliceType$25 = $sliceType($Uint8);
	sliceType$26 = $sliceType($Uint8);
	FormatInt = $pkg.FormatInt = function(i, base) {
		var _tuple, base, i, s;
		_tuple = formatBits(sliceType$23.nil, new $Uint64(i.$high, i.$low), base, (i.$high < 0 || (i.$high === 0 && i.$low < 0)), false); s = _tuple[1];
		return s;
	};
	Itoa = $pkg.Itoa = function(i) {
		var i;
		return FormatInt(new $Int64(0, i), 10);
	};
	formatBits = function(dst, u, base, neg, append_) {
		var a, append_, b, b$1, base, d = sliceType$24.nil, dst, i, j, m, neg, q, q$1, s = "", s$1, u, x, x$1, x$2, x$3;
		if (base < 2 || base > 36) {
			$panic(new $String("strconv: illegal AppendInt/FormatInt base"));
		}
		a = $clone(arrayType$7.zero(), arrayType$7);
		i = 65;
		if (neg) {
			u = new $Uint64(-u.$high, -u.$low);
		}
		if (base === 10) {
			while (true) {
				if (!((u.$high > 0 || (u.$high === 0 && u.$low >= 100)))) { break; }
				i = i - (2) >> 0;
				q = $div64(u, new $Uint64(0, 100), false);
				j = ((x = $mul64(q, new $Uint64(0, 100)), new $Uint64(u.$high - x.$high, u.$low - x.$low)).$low >>> 0);
				(x$1 = i + 1 >> 0, ((x$1 < 0 || x$1 >= a.length) ? $throwRuntimeError("index out of range") : a[x$1] = "0123456789012345678901234567890123456789012345678901234567890123456789012345678901234567890123456789".charCodeAt(j)));
				(x$2 = i + 0 >> 0, ((x$2 < 0 || x$2 >= a.length) ? $throwRuntimeError("index out of range") : a[x$2] = "0000000000111111111122222222223333333333444444444455555555556666666666777777777788888888889999999999".charCodeAt(j)));
				u = q;
			}
			if ((u.$high > 0 || (u.$high === 0 && u.$low >= 10))) {
				i = i - (1) >> 0;
				q$1 = $div64(u, new $Uint64(0, 10), false);
				((i < 0 || i >= a.length) ? $throwRuntimeError("index out of range") : a[i] = "0123456789abcdefghijklmnopqrstuvwxyz".charCodeAt(((x$3 = $mul64(q$1, new $Uint64(0, 10)), new $Uint64(u.$high - x$3.$high, u.$low - x$3.$low)).$low >>> 0)));
				u = q$1;
			}
		} else {
			s$1 = ((base < 0 || base >= shifts.length) ? $throwRuntimeError("index out of range") : shifts[base]);
			if (s$1 > 0) {
				b = new $Uint64(0, base);
				m = (b.$low >>> 0) - 1 >>> 0;
				while (true) {
					if (!((u.$high > b.$high || (u.$high === b.$high && u.$low >= b.$low)))) { break; }
					i = i - (1) >> 0;
					((i < 0 || i >= a.length) ? $throwRuntimeError("index out of range") : a[i] = "0123456789abcdefghijklmnopqrstuvwxyz".charCodeAt((((u.$low >>> 0) & m) >>> 0)));
					u = $shiftRightUint64(u, (s$1));
				}
			} else {
				b$1 = new $Uint64(0, base);
				while (true) {
					if (!((u.$high > b$1.$high || (u.$high === b$1.$high && u.$low >= b$1.$low)))) { break; }
					i = i - (1) >> 0;
					((i < 0 || i >= a.length) ? $throwRuntimeError("index out of range") : a[i] = "0123456789abcdefghijklmnopqrstuvwxyz".charCodeAt(($div64(u, b$1, true).$low >>> 0)));
					u = $div64(u, (b$1), false);
				}
			}
		}
		i = i - (1) >> 0;
		((i < 0 || i >= a.length) ? $throwRuntimeError("index out of range") : a[i] = "0123456789abcdefghijklmnopqrstuvwxyz".charCodeAt((u.$low >>> 0)));
		if (neg) {
			i = i - (1) >> 0;
			((i < 0 || i >= a.length) ? $throwRuntimeError("index out of range") : a[i] = 45);
		}
		if (append_) {
			d = $appendSlice(dst, $subslice(new sliceType$25(a), i));
			return [d, s];
		}
		s = $bytesToString($subslice(new sliceType$26(a), i));
		return [d, s];
	};
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_strconv = function() { while (true) { switch ($s) { case 0:
		$r = errors.$init($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		$r = math.$init($BLOCKING); /* */ $s = 2; case 2: if ($r && $r.$blocking) { $r = $r(); }
		$r = utf8.$init($BLOCKING); /* */ $s = 3; case 3: if ($r && $r.$blocking) { $r = $r(); }
		$pkg.ErrRange = errors.New("value out of range");
		$pkg.ErrSyntax = errors.New("invalid syntax");
		shifts = $toNativeArray($kindUint, [0, 0, 1, 0, 2, 0, 0, 0, 3, 0, 0, 0, 0, 0, 0, 0, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 5, 0, 0, 0, 0]);
		/* */ } return; } }; $init_strconv.$blocking = true; return $init_strconv;
	};
	return $pkg;
})();
$packages["sync/atomic"] = (function() {
	var $pkg = {}, js, CompareAndSwapInt32, AddInt32, LoadUint32, StoreUint32;
	js = $packages["github.com/gopherjs/gopherjs/js"];
	CompareAndSwapInt32 = $pkg.CompareAndSwapInt32 = function(addr, old, new$1) {
		var addr, new$1, old;
		if (addr.$get() === old) {
			addr.$set(new$1);
			return true;
		}
		return false;
	};
	AddInt32 = $pkg.AddInt32 = function(addr, delta) {
		var addr, delta, new$1;
		new$1 = addr.$get() + delta >> 0;
		addr.$set(new$1);
		return new$1;
	};
	LoadUint32 = $pkg.LoadUint32 = function(addr) {
		var addr;
		return addr.$get();
	};
	StoreUint32 = $pkg.StoreUint32 = function(addr, val) {
		var addr, val;
		addr.$set(val);
	};
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_atomic = function() { while (true) { switch ($s) { case 0:
		$r = js.$init($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		/* */ } return; } }; $init_atomic.$blocking = true; return $init_atomic;
	};
	return $pkg;
})();
$packages["sync"] = (function() {
	var $pkg = {}, runtime, atomic, Pool, Mutex, Locker, Once, poolLocal, syncSema, RWMutex, rlocker, ptrType, sliceType, structType, chanType, structType$1, chanType$1, sliceType$1, structType$2, ptrType$6, ptrType$7, ptrType$8, ptrType$9, ptrType$10, ptrType$11, ptrType$12, ptrType$13, ptrType$16, sliceType$3, ptrType$19, sliceType$4, ptrType$20, ptrType$21, ptrType$22, ptrType$23, ptrType$24, ptrType$25, ptrType$26, ptrType$27, ptrType$28, ptrType$29, ptrType$30, ptrType$31, ptrType$32, sliceType$5, ptrType$42, ptrType$43, funcType, ptrType$46, funcType$1, ptrType$47, arrayType, ptrType$48, ptrType$49, semWaiters, allPools, runtime_registerPoolCleanup, runtime_Semacquire, runtime_Semrelease, runtime_Syncsemcheck, poolCleanup, init, indexLocal, raceEnable, init$1;
	runtime = $packages["runtime"];
	atomic = $packages["sync/atomic"];
	Pool = $pkg.Pool = $newType(0, $kindStruct, "sync.Pool", "Pool", "sync", function(local_, localSize_, store_, New_) {
		this.$val = this;
		this.local = local_ !== undefined ? local_ : 0;
		this.localSize = localSize_ !== undefined ? localSize_ : 0;
		this.store = store_ !== undefined ? store_ : sliceType$5.nil;
		this.New = New_ !== undefined ? New_ : $throwNilPointerError;
	});
	Mutex = $pkg.Mutex = $newType(0, $kindStruct, "sync.Mutex", "Mutex", "sync", function(state_, sema_) {
		this.$val = this;
		this.state = state_ !== undefined ? state_ : 0;
		this.sema = sema_ !== undefined ? sema_ : 0;
	});
	Locker = $pkg.Locker = $newType(8, $kindInterface, "sync.Locker", "Locker", "sync", null);
	Once = $pkg.Once = $newType(0, $kindStruct, "sync.Once", "Once", "sync", function(m_, done_) {
		this.$val = this;
		this.m = m_ !== undefined ? m_ : new Mutex.ptr();
		this.done = done_ !== undefined ? done_ : 0;
	});
	poolLocal = $pkg.poolLocal = $newType(0, $kindStruct, "sync.poolLocal", "poolLocal", "sync", function(private$0_, shared_, Mutex_, pad_) {
		this.$val = this;
		this.private$0 = private$0_ !== undefined ? private$0_ : $ifaceNil;
		this.shared = shared_ !== undefined ? shared_ : sliceType$3.nil;
		this.Mutex = Mutex_ !== undefined ? Mutex_ : new Mutex.ptr();
		this.pad = pad_ !== undefined ? pad_ : arrayType.zero();
	});
	syncSema = $pkg.syncSema = $newType(0, $kindStruct, "sync.syncSema", "syncSema", "sync", function(lock_, head_, tail_) {
		this.$val = this;
		this.lock = lock_ !== undefined ? lock_ : 0;
		this.head = head_ !== undefined ? head_ : 0;
		this.tail = tail_ !== undefined ? tail_ : 0;
	});
	RWMutex = $pkg.RWMutex = $newType(0, $kindStruct, "sync.RWMutex", "RWMutex", "sync", function(w_, writerSem_, readerSem_, readerCount_, readerWait_) {
		this.$val = this;
		this.w = w_ !== undefined ? w_ : new Mutex.ptr();
		this.writerSem = writerSem_ !== undefined ? writerSem_ : 0;
		this.readerSem = readerSem_ !== undefined ? readerSem_ : 0;
		this.readerCount = readerCount_ !== undefined ? readerCount_ : 0;
		this.readerWait = readerWait_ !== undefined ? readerWait_ : 0;
	});
	rlocker = $pkg.rlocker = $newType(0, $kindStruct, "sync.rlocker", "rlocker", "sync", function(w_, writerSem_, readerSem_, readerCount_, readerWait_) {
		this.$val = this;
		this.w = w_ !== undefined ? w_ : new Mutex.ptr();
		this.writerSem = writerSem_ !== undefined ? writerSem_ : 0;
		this.readerSem = readerSem_ !== undefined ? readerSem_ : 0;
		this.readerCount = readerCount_ !== undefined ? readerCount_ : 0;
		this.readerWait = readerWait_ !== undefined ? readerWait_ : 0;
	});
	ptrType = $ptrType(Pool);
	sliceType = $sliceType(ptrType);
	structType = $structType([]);
	chanType = $chanType(structType, false, false);
	structType$1 = $structType([]);
	chanType$1 = $chanType(structType$1, false, false);
	sliceType$1 = $sliceType(chanType$1);
	structType$2 = $structType([]);
	ptrType$6 = $ptrType($Int32);
	ptrType$7 = $ptrType($Int32);
	ptrType$8 = $ptrType($Uint32);
	ptrType$9 = $ptrType($Int32);
	ptrType$10 = $ptrType($Int32);
	ptrType$11 = $ptrType($Uint32);
	ptrType$12 = $ptrType($Uint32);
	ptrType$13 = $ptrType($Uint32);
	ptrType$16 = $ptrType(poolLocal);
	sliceType$3 = $sliceType($emptyInterface);
	ptrType$19 = $ptrType(Pool);
	sliceType$4 = $sliceType(ptrType$19);
	ptrType$20 = $ptrType($Int32);
	ptrType$21 = $ptrType($Uint32);
	ptrType$22 = $ptrType($Int32);
	ptrType$23 = $ptrType($Int32);
	ptrType$24 = $ptrType($Uint32);
	ptrType$25 = $ptrType($Int32);
	ptrType$26 = $ptrType($Int32);
	ptrType$27 = $ptrType($Uint32);
	ptrType$28 = $ptrType($Int32);
	ptrType$29 = $ptrType($Uint32);
	ptrType$30 = $ptrType(rlocker);
	ptrType$31 = $ptrType(RWMutex);
	ptrType$32 = $ptrType(RWMutex);
	sliceType$5 = $sliceType($emptyInterface);
	ptrType$42 = $ptrType(poolLocal);
	ptrType$43 = $ptrType(Pool);
	funcType = $funcType([], [$emptyInterface], false);
	ptrType$46 = $ptrType(Mutex);
	funcType$1 = $funcType([], [], false);
	ptrType$47 = $ptrType(Once);
	arrayType = $arrayType($Uint8, 128);
	ptrType$48 = $ptrType(RWMutex);
	ptrType$49 = $ptrType(rlocker);
	Pool.ptr.prototype.Get = function() {
		var p, x, x$1, x$2;
		p = this;
		if (p.store.$length === 0) {
			if (!(p.New === $throwNilPointerError)) {
				return p.New();
			}
			return $ifaceNil;
		}
		x$2 = (x = p.store, x$1 = p.store.$length - 1 >> 0, ((x$1 < 0 || x$1 >= x.$length) ? $throwRuntimeError("index out of range") : x.$array[x.$offset + x$1]));
		p.store = $subslice(p.store, 0, (p.store.$length - 1 >> 0));
		return x$2;
	};
	Pool.prototype.Get = function() { return this.$val.Get(); };
	Pool.ptr.prototype.Put = function(x) {
		var p, x;
		p = this;
		if ($interfaceIsEqual(x, $ifaceNil)) {
			return;
		}
		p.store = $append(p.store, x);
	};
	Pool.prototype.Put = function(x) { return this.$val.Put(x); };
	runtime_registerPoolCleanup = function(cleanup) {
		var cleanup;
	};
	runtime_Semacquire = function(s, $b) {
		var $args = arguments, $r, $s = 0, $this = this, _entry, _key, _r, ch;
		/* */ if($b !== $BLOCKING) { $nonblockingCall(); }; var $blocking_runtime_Semacquire = function() { s: while (true) { switch ($s) { case 0:
		/* */ if (s.$get() === 0) { $s = 1; continue; }
		/* */ $s = 2; continue;
		/* if (s.$get() === 0) { */ case 1:
			ch = new chanType(0);
			_key = s; (semWaiters || $throwRuntimeError("assignment to entry in nil map"))[_key.$key()] = { k: _key, v: $append((_entry = semWaiters[s.$key()], _entry !== undefined ? _entry.v : sliceType$1.nil), ch) };
			_r = $recv(ch, $BLOCKING); /* */ $s = 3; case 3: if (_r && _r.$blocking) { _r = _r(); }
			_r[0];
		/* } */ case 2:
		s.$set(s.$get() - (1) >>> 0);
		/* */ case -1: } return; } }; $blocking_runtime_Semacquire.$blocking = true; return $blocking_runtime_Semacquire;
	};
	runtime_Semrelease = function(s, $b) {
		var $args = arguments, $r, $s = 0, $this = this, _entry, _key, ch, w;
		/* */ if($b !== $BLOCKING) { $nonblockingCall(); }; var $blocking_runtime_Semrelease = function() { s: while (true) { switch ($s) { case 0:
		s.$set(s.$get() + (1) >>> 0);
		w = (_entry = semWaiters[s.$key()], _entry !== undefined ? _entry.v : sliceType$1.nil);
		if (w.$length === 0) {
			return;
		}
		ch = (0 >= w.$length ? $throwRuntimeError("index out of range") : w.$array[w.$offset + 0]);
		w = $subslice(w, 1);
		_key = s; (semWaiters || $throwRuntimeError("assignment to entry in nil map"))[_key.$key()] = { k: _key, v: w };
		if (w.$length === 0) {
			delete semWaiters[s.$key()];
		}
		$r = $send(ch, new structType$2.ptr(), $BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		/* */ case -1: } return; } }; $blocking_runtime_Semrelease.$blocking = true; return $blocking_runtime_Semrelease;
	};
	runtime_Syncsemcheck = function(size) {
		var size;
	};
	Mutex.ptr.prototype.Lock = function($b) {
		var $args = arguments, $r, $s = 0, $this = this, awoke, m, new$1, old;
		/* */ if($b !== $BLOCKING) { $nonblockingCall(); }; var $blocking_Lock = function() { s: while (true) { switch ($s) { case 0:
		m = $this;
		if (atomic.CompareAndSwapInt32(new ptrType$6(function() { return this.$target.state; }, function($v) { this.$target.state = $v; }, m), 0, 1)) {
			return;
		}
		awoke = false;
		/* while (true) { */ case 1:
			old = m.state;
			new$1 = old | 1;
			if (!(((old & 1) === 0))) {
				new$1 = old + 4 >> 0;
			}
			if (awoke) {
				new$1 = new$1 & ~(2);
			}
			/* */ if (atomic.CompareAndSwapInt32(new ptrType$7(function() { return this.$target.state; }, function($v) { this.$target.state = $v; }, m), old, new$1)) { $s = 3; continue; }
			/* */ $s = 4; continue;
			/* if (atomic.CompareAndSwapInt32(new ptrType$7(function() { return this.$target.state; }, function($v) { this.$target.state = $v; }, m), old, new$1)) { */ case 3:
				if ((old & 1) === 0) {
					/* break; */ $s = 2; continue;
				}
				$r = runtime_Semacquire(new ptrType$8(function() { return this.$target.sema; }, function($v) { this.$target.sema = $v; }, m), $BLOCKING); /* */ $s = 5; case 5: if ($r && $r.$blocking) { $r = $r(); }
				awoke = true;
			/* } */ case 4:
		/* } */ $s = 1; continue; case 2:
		/* */ case -1: } return; } }; $blocking_Lock.$blocking = true; return $blocking_Lock;
	};
	Mutex.prototype.Lock = function($b) { return this.$val.Lock($b); };
	Mutex.ptr.prototype.Unlock = function($b) {
		var $args = arguments, $r, $s = 0, $this = this, m, new$1, old;
		/* */ if($b !== $BLOCKING) { $nonblockingCall(); }; var $blocking_Unlock = function() { s: while (true) { switch ($s) { case 0:
		m = $this;
		new$1 = atomic.AddInt32(new ptrType$9(function() { return this.$target.state; }, function($v) { this.$target.state = $v; }, m), -1);
		if ((((new$1 + 1 >> 0)) & 1) === 0) {
			$panic(new $String("sync: unlock of unlocked mutex"));
		}
		old = new$1;
		/* while (true) { */ case 1:
			if (((old >> 2 >> 0) === 0) || !(((old & 3) === 0))) {
				return;
			}
			new$1 = ((old - 4 >> 0)) | 2;
			/* */ if (atomic.CompareAndSwapInt32(new ptrType$10(function() { return this.$target.state; }, function($v) { this.$target.state = $v; }, m), old, new$1)) { $s = 3; continue; }
			/* */ $s = 4; continue;
			/* if (atomic.CompareAndSwapInt32(new ptrType$10(function() { return this.$target.state; }, function($v) { this.$target.state = $v; }, m), old, new$1)) { */ case 3:
				$r = runtime_Semrelease(new ptrType$11(function() { return this.$target.sema; }, function($v) { this.$target.sema = $v; }, m), $BLOCKING); /* */ $s = 5; case 5: if ($r && $r.$blocking) { $r = $r(); }
				return;
			/* } */ case 4:
			old = m.state;
		/* } */ $s = 1; continue; case 2:
		/* */ case -1: } return; } }; $blocking_Unlock.$blocking = true; return $blocking_Unlock;
	};
	Mutex.prototype.Unlock = function($b) { return this.$val.Unlock($b); };
	Once.ptr.prototype.Do = function(f, $b) {
		var $args = arguments, $deferred = [], $err = null, $r, $s = 0, $this = this, o;
		/* */ if($b !== $BLOCKING) { $nonblockingCall(); }; var $blocking_Do = function() { try { $deferFrames.push($deferred); s: while (true) { switch ($s) { case 0:
		o = $this;
		if (atomic.LoadUint32(new ptrType$12(function() { return this.$target.done; }, function($v) { this.$target.done = $v; }, o)) === 1) {
			return;
		}
		$r = o.m.Lock($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		$deferred.push([$methodVal(o.m, "Unlock"), [$BLOCKING]]);
		if (o.done === 0) {
			$deferred.push([atomic.StoreUint32, [new ptrType$13(function() { return this.$target.done; }, function($v) { this.$target.done = $v; }, o), 1, $BLOCKING]]);
			f();
		}
		/* */ case -1: } return; } } catch(err) { $err = err; } finally { $deferFrames.pop(); if ($curGoroutine.asleep && !$jumpToDefer) { throw null; } $s = -1; $callDeferred($deferred, $err); } }; $blocking_Do.$blocking = true; return $blocking_Do;
	};
	Once.prototype.Do = function(f, $b) { return this.$val.Do(f, $b); };
	poolCleanup = function() {
		var _i, _i$1, _ref, _ref$1, i, i$1, j, l, p, x;
		_ref = allPools;
		_i = 0;
		while (true) {
			if (!(_i < _ref.$length)) { break; }
			i = _i;
			p = ((_i < 0 || _i >= _ref.$length) ? $throwRuntimeError("index out of range") : _ref.$array[_ref.$offset + _i]);
			((i < 0 || i >= allPools.$length) ? $throwRuntimeError("index out of range") : allPools.$array[allPools.$offset + i] = ptrType.nil);
			i$1 = 0;
			while (true) {
				if (!(i$1 < (p.localSize >> 0))) { break; }
				l = indexLocal(p.local, i$1);
				l.private$0 = $ifaceNil;
				_ref$1 = l.shared;
				_i$1 = 0;
				while (true) {
					if (!(_i$1 < _ref$1.$length)) { break; }
					j = _i$1;
					(x = l.shared, ((j < 0 || j >= x.$length) ? $throwRuntimeError("index out of range") : x.$array[x.$offset + j] = $ifaceNil));
					_i$1++;
				}
				l.shared = sliceType$3.nil;
				i$1 = i$1 + (1) >> 0;
			}
			p.local = 0;
			p.localSize = 0;
			_i++;
		}
		allPools = new sliceType$4([]);
	};
	init = function() {
		runtime_registerPoolCleanup(poolCleanup);
	};
	indexLocal = function(l, i) {
		var i, l, x;
		return (x = l, (x.nilCheck, ((i < 0 || i >= x.length) ? $throwRuntimeError("index out of range") : x[i])));
	};
	raceEnable = function() {
	};
	init$1 = function() {
		var s;
		s = $clone(new syncSema.ptr(), syncSema);
		runtime_Syncsemcheck(12);
	};
	RWMutex.ptr.prototype.RLock = function($b) {
		var $args = arguments, $r, $s = 0, $this = this, rw;
		/* */ if($b !== $BLOCKING) { $nonblockingCall(); }; var $blocking_RLock = function() { s: while (true) { switch ($s) { case 0:
		rw = $this;
		/* */ if (atomic.AddInt32(new ptrType$20(function() { return this.$target.readerCount; }, function($v) { this.$target.readerCount = $v; }, rw), 1) < 0) { $s = 1; continue; }
		/* */ $s = 2; continue;
		/* if (atomic.AddInt32(new ptrType$20(function() { return this.$target.readerCount; }, function($v) { this.$target.readerCount = $v; }, rw), 1) < 0) { */ case 1:
			$r = runtime_Semacquire(new ptrType$21(function() { return this.$target.readerSem; }, function($v) { this.$target.readerSem = $v; }, rw), $BLOCKING); /* */ $s = 3; case 3: if ($r && $r.$blocking) { $r = $r(); }
		/* } */ case 2:
		/* */ case -1: } return; } }; $blocking_RLock.$blocking = true; return $blocking_RLock;
	};
	RWMutex.prototype.RLock = function($b) { return this.$val.RLock($b); };
	RWMutex.ptr.prototype.RUnlock = function($b) {
		var $args = arguments, $r, $s = 0, $this = this, r, rw;
		/* */ if($b !== $BLOCKING) { $nonblockingCall(); }; var $blocking_RUnlock = function() { s: while (true) { switch ($s) { case 0:
		rw = $this;
		r = atomic.AddInt32(new ptrType$22(function() { return this.$target.readerCount; }, function($v) { this.$target.readerCount = $v; }, rw), -1);
		/* */ if (r < 0) { $s = 1; continue; }
		/* */ $s = 2; continue;
		/* if (r < 0) { */ case 1:
			if (((r + 1 >> 0) === 0) || ((r + 1 >> 0) === -1073741824)) {
				raceEnable();
				$panic(new $String("sync: RUnlock of unlocked RWMutex"));
			}
			/* */ if (atomic.AddInt32(new ptrType$23(function() { return this.$target.readerWait; }, function($v) { this.$target.readerWait = $v; }, rw), -1) === 0) { $s = 3; continue; }
			/* */ $s = 4; continue;
			/* if (atomic.AddInt32(new ptrType$23(function() { return this.$target.readerWait; }, function($v) { this.$target.readerWait = $v; }, rw), -1) === 0) { */ case 3:
				$r = runtime_Semrelease(new ptrType$24(function() { return this.$target.writerSem; }, function($v) { this.$target.writerSem = $v; }, rw), $BLOCKING); /* */ $s = 5; case 5: if ($r && $r.$blocking) { $r = $r(); }
			/* } */ case 4:
		/* } */ case 2:
		/* */ case -1: } return; } }; $blocking_RUnlock.$blocking = true; return $blocking_RUnlock;
	};
	RWMutex.prototype.RUnlock = function($b) { return this.$val.RUnlock($b); };
	RWMutex.ptr.prototype.Lock = function($b) {
		var $args = arguments, $r, $s = 0, $this = this, r, rw;
		/* */ if($b !== $BLOCKING) { $nonblockingCall(); }; var $blocking_Lock = function() { s: while (true) { switch ($s) { case 0:
		rw = $this;
		$r = rw.w.Lock($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		r = atomic.AddInt32(new ptrType$25(function() { return this.$target.readerCount; }, function($v) { this.$target.readerCount = $v; }, rw), -1073741824) + 1073741824 >> 0;
		/* */ if (!((r === 0)) && !((atomic.AddInt32(new ptrType$26(function() { return this.$target.readerWait; }, function($v) { this.$target.readerWait = $v; }, rw), r) === 0))) { $s = 2; continue; }
		/* */ $s = 3; continue;
		/* if (!((r === 0)) && !((atomic.AddInt32(new ptrType$26(function() { return this.$target.readerWait; }, function($v) { this.$target.readerWait = $v; }, rw), r) === 0))) { */ case 2:
			$r = runtime_Semacquire(new ptrType$27(function() { return this.$target.writerSem; }, function($v) { this.$target.writerSem = $v; }, rw), $BLOCKING); /* */ $s = 4; case 4: if ($r && $r.$blocking) { $r = $r(); }
		/* } */ case 3:
		/* */ case -1: } return; } }; $blocking_Lock.$blocking = true; return $blocking_Lock;
	};
	RWMutex.prototype.Lock = function($b) { return this.$val.Lock($b); };
	RWMutex.ptr.prototype.Unlock = function($b) {
		var $args = arguments, $r, $s = 0, $this = this, i, r, rw;
		/* */ if($b !== $BLOCKING) { $nonblockingCall(); }; var $blocking_Unlock = function() { s: while (true) { switch ($s) { case 0:
		rw = $this;
		r = atomic.AddInt32(new ptrType$28(function() { return this.$target.readerCount; }, function($v) { this.$target.readerCount = $v; }, rw), 1073741824);
		if (r >= 1073741824) {
			raceEnable();
			$panic(new $String("sync: Unlock of unlocked RWMutex"));
		}
		i = 0;
		/* while (true) { */ case 1:
			/* if (!(i < (r >> 0))) { break; } */ if(!(i < (r >> 0))) { $s = 2; continue; }
			$r = runtime_Semrelease(new ptrType$29(function() { return this.$target.readerSem; }, function($v) { this.$target.readerSem = $v; }, rw), $BLOCKING); /* */ $s = 3; case 3: if ($r && $r.$blocking) { $r = $r(); }
			i = i + (1) >> 0;
		/* } */ $s = 1; continue; case 2:
		$r = rw.w.Unlock($BLOCKING); /* */ $s = 4; case 4: if ($r && $r.$blocking) { $r = $r(); }
		/* */ case -1: } return; } }; $blocking_Unlock.$blocking = true; return $blocking_Unlock;
	};
	RWMutex.prototype.Unlock = function($b) { return this.$val.Unlock($b); };
	RWMutex.ptr.prototype.RLocker = function() {
		var rw;
		rw = this;
		return $pointerOfStructConversion(rw, ptrType$30);
	};
	RWMutex.prototype.RLocker = function() { return this.$val.RLocker(); };
	rlocker.ptr.prototype.Lock = function($b) {
		var $args = arguments, $r, $s = 0, $this = this, r;
		/* */ if($b !== $BLOCKING) { $nonblockingCall(); }; var $blocking_Lock = function() { s: while (true) { switch ($s) { case 0:
		r = $this;
		$r = $pointerOfStructConversion(r, ptrType$31).RLock($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		/* */ case -1: } return; } }; $blocking_Lock.$blocking = true; return $blocking_Lock;
	};
	rlocker.prototype.Lock = function($b) { return this.$val.Lock($b); };
	rlocker.ptr.prototype.Unlock = function($b) {
		var $args = arguments, $r, $s = 0, $this = this, r;
		/* */ if($b !== $BLOCKING) { $nonblockingCall(); }; var $blocking_Unlock = function() { s: while (true) { switch ($s) { case 0:
		r = $this;
		$r = $pointerOfStructConversion(r, ptrType$32).RUnlock($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		/* */ case -1: } return; } }; $blocking_Unlock.$blocking = true; return $blocking_Unlock;
	};
	rlocker.prototype.Unlock = function($b) { return this.$val.Unlock($b); };
	ptrType$43.methods = [{prop: "Get", name: "Get", pkg: "", typ: $funcType([], [$emptyInterface], false)}, {prop: "Put", name: "Put", pkg: "", typ: $funcType([$emptyInterface], [], false)}, {prop: "getSlow", name: "getSlow", pkg: "sync", typ: $funcType([], [$emptyInterface], false)}, {prop: "pin", name: "pin", pkg: "sync", typ: $funcType([], [ptrType$42], false)}, {prop: "pinSlow", name: "pinSlow", pkg: "sync", typ: $funcType([], [ptrType$16], false)}];
	ptrType$46.methods = [{prop: "Lock", name: "Lock", pkg: "", typ: $funcType([], [], false)}, {prop: "Unlock", name: "Unlock", pkg: "", typ: $funcType([], [], false)}];
	ptrType$47.methods = [{prop: "Do", name: "Do", pkg: "", typ: $funcType([funcType$1], [], false)}];
	ptrType$48.methods = [{prop: "RLock", name: "RLock", pkg: "", typ: $funcType([], [], false)}, {prop: "RUnlock", name: "RUnlock", pkg: "", typ: $funcType([], [], false)}, {prop: "Lock", name: "Lock", pkg: "", typ: $funcType([], [], false)}, {prop: "Unlock", name: "Unlock", pkg: "", typ: $funcType([], [], false)}, {prop: "RLocker", name: "RLocker", pkg: "", typ: $funcType([], [Locker], false)}];
	ptrType$49.methods = [{prop: "Lock", name: "Lock", pkg: "", typ: $funcType([], [], false)}, {prop: "Unlock", name: "Unlock", pkg: "", typ: $funcType([], [], false)}];
	Pool.init([{prop: "local", name: "local", pkg: "sync", typ: $UnsafePointer, tag: ""}, {prop: "localSize", name: "localSize", pkg: "sync", typ: $Uintptr, tag: ""}, {prop: "store", name: "store", pkg: "sync", typ: sliceType$5, tag: ""}, {prop: "New", name: "New", pkg: "", typ: funcType, tag: ""}]);
	Mutex.init([{prop: "state", name: "state", pkg: "sync", typ: $Int32, tag: ""}, {prop: "sema", name: "sema", pkg: "sync", typ: $Uint32, tag: ""}]);
	Locker.init([{prop: "Lock", name: "Lock", pkg: "", typ: $funcType([], [], false)}, {prop: "Unlock", name: "Unlock", pkg: "", typ: $funcType([], [], false)}]);
	Once.init([{prop: "m", name: "m", pkg: "sync", typ: Mutex, tag: ""}, {prop: "done", name: "done", pkg: "sync", typ: $Uint32, tag: ""}]);
	poolLocal.init([{prop: "private$0", name: "private", pkg: "sync", typ: $emptyInterface, tag: ""}, {prop: "shared", name: "shared", pkg: "sync", typ: sliceType$3, tag: ""}, {prop: "Mutex", name: "", pkg: "", typ: Mutex, tag: ""}, {prop: "pad", name: "pad", pkg: "sync", typ: arrayType, tag: ""}]);
	syncSema.init([{prop: "lock", name: "lock", pkg: "sync", typ: $Uintptr, tag: ""}, {prop: "head", name: "head", pkg: "sync", typ: $UnsafePointer, tag: ""}, {prop: "tail", name: "tail", pkg: "sync", typ: $UnsafePointer, tag: ""}]);
	RWMutex.init([{prop: "w", name: "w", pkg: "sync", typ: Mutex, tag: ""}, {prop: "writerSem", name: "writerSem", pkg: "sync", typ: $Uint32, tag: ""}, {prop: "readerSem", name: "readerSem", pkg: "sync", typ: $Uint32, tag: ""}, {prop: "readerCount", name: "readerCount", pkg: "sync", typ: $Int32, tag: ""}, {prop: "readerWait", name: "readerWait", pkg: "sync", typ: $Int32, tag: ""}]);
	rlocker.init([{prop: "w", name: "w", pkg: "sync", typ: Mutex, tag: ""}, {prop: "writerSem", name: "writerSem", pkg: "sync", typ: $Uint32, tag: ""}, {prop: "readerSem", name: "readerSem", pkg: "sync", typ: $Uint32, tag: ""}, {prop: "readerCount", name: "readerCount", pkg: "sync", typ: $Int32, tag: ""}, {prop: "readerWait", name: "readerWait", pkg: "sync", typ: $Int32, tag: ""}]);
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_sync = function() { while (true) { switch ($s) { case 0:
		$r = runtime.$init($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		$r = atomic.$init($BLOCKING); /* */ $s = 2; case 2: if ($r && $r.$blocking) { $r = $r(); }
		allPools = sliceType.nil;
		semWaiters = new $Map();
		init();
		init$1();
		/* */ } return; } }; $init_sync.$blocking = true; return $init_sync;
	};
	return $pkg;
})();
$packages["io"] = (function() {
	var $pkg = {}, errors, runtime, sync, errWhence, errOffset;
	errors = $packages["errors"];
	runtime = $packages["runtime"];
	sync = $packages["sync"];
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_io = function() { while (true) { switch ($s) { case 0:
		$r = errors.$init($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		$r = runtime.$init($BLOCKING); /* */ $s = 2; case 2: if ($r && $r.$blocking) { $r = $r(); }
		$r = sync.$init($BLOCKING); /* */ $s = 3; case 3: if ($r && $r.$blocking) { $r = $r(); }
		$pkg.ErrShortWrite = errors.New("short write");
		$pkg.ErrShortBuffer = errors.New("short buffer");
		$pkg.EOF = errors.New("EOF");
		$pkg.ErrUnexpectedEOF = errors.New("unexpected EOF");
		$pkg.ErrNoProgress = errors.New("multiple Read calls return no data or error");
		errWhence = errors.New("Seek: invalid whence");
		errOffset = errors.New("Seek: invalid offset");
		$pkg.ErrClosedPipe = errors.New("io: read/write on closed pipe");
		/* */ } return; } }; $init_io.$blocking = true; return $init_io;
	};
	return $pkg;
})();
$packages["unicode"] = (function() {
	var $pkg = {};
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_unicode = function() { while (true) { switch ($s) { case 0:
		/* */ } return; } }; $init_unicode.$blocking = true; return $init_unicode;
	};
	return $pkg;
})();
$packages["strings"] = (function() {
	var $pkg = {}, errors, js, io, unicode, utf8, sliceType$18, IndexByte, Join;
	errors = $packages["errors"];
	js = $packages["github.com/gopherjs/gopherjs/js"];
	io = $packages["io"];
	unicode = $packages["unicode"];
	utf8 = $packages["unicode/utf8"];
	sliceType$18 = $sliceType($Uint8);
	IndexByte = $pkg.IndexByte = function(s, c) {
		var c, s;
		return $parseInt(s.indexOf($global.String.fromCharCode(c))) >> 0;
	};
	Join = $pkg.Join = function(a, sep) {
		var _i, _ref, a, b, bp, i, n, s, sep;
		if (a.$length === 0) {
			return "";
		}
		if (a.$length === 1) {
			return (0 >= a.$length ? $throwRuntimeError("index out of range") : a.$array[a.$offset + 0]);
		}
		n = sep.length * ((a.$length - 1 >> 0)) >> 0;
		i = 0;
		while (true) {
			if (!(i < a.$length)) { break; }
			n = n + (((i < 0 || i >= a.$length) ? $throwRuntimeError("index out of range") : a.$array[a.$offset + i]).length) >> 0;
			i = i + (1) >> 0;
		}
		b = $makeSlice(sliceType$18, n);
		bp = $copyString(b, (0 >= a.$length ? $throwRuntimeError("index out of range") : a.$array[a.$offset + 0]));
		_ref = $subslice(a, 1);
		_i = 0;
		while (true) {
			if (!(_i < _ref.$length)) { break; }
			s = ((_i < 0 || _i >= _ref.$length) ? $throwRuntimeError("index out of range") : _ref.$array[_ref.$offset + _i]);
			bp = bp + ($copyString($subslice(b, bp), sep)) >> 0;
			bp = bp + ($copyString($subslice(b, bp), s)) >> 0;
			_i++;
		}
		return $bytesToString(b);
	};
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_strings = function() { while (true) { switch ($s) { case 0:
		$r = errors.$init($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		$r = js.$init($BLOCKING); /* */ $s = 2; case 2: if ($r && $r.$blocking) { $r = $r(); }
		$r = io.$init($BLOCKING); /* */ $s = 3; case 3: if ($r && $r.$blocking) { $r = $r(); }
		$r = unicode.$init($BLOCKING); /* */ $s = 4; case 4: if ($r && $r.$blocking) { $r = $r(); }
		$r = utf8.$init($BLOCKING); /* */ $s = 5; case 5: if ($r && $r.$blocking) { $r = $r(); }
		/* */ } return; } }; $init_strings.$blocking = true; return $init_strings;
	};
	return $pkg;
})();
$packages["github.com/gopherjs/gopherjs/nosync"] = (function() {
	var $pkg = {}, Once, funcType, ptrType$3;
	Once = $pkg.Once = $newType(0, $kindStruct, "nosync.Once", "Once", "github.com/gopherjs/gopherjs/nosync", function(doing_, done_) {
		this.$val = this;
		this.doing = doing_ !== undefined ? doing_ : false;
		this.done = done_ !== undefined ? done_ : false;
	});
	funcType = $funcType([], [], false);
	ptrType$3 = $ptrType(Once);
	Once.ptr.prototype.Do = function(f) {
		var $deferred = [], $err = null, f, o;
		/* */ try { $deferFrames.push($deferred);
		o = this;
		if (o.done) {
			return;
		}
		if (o.doing) {
			$panic(new $String("nosync: Do called within f"));
		}
		o.doing = true;
		$deferred.push([(function() {
			o.doing = false;
			o.done = true;
		}), []]);
		f();
		/* */ } catch(err) { $err = err; } finally { $deferFrames.pop(); $callDeferred($deferred, $err); }
	};
	Once.prototype.Do = function(f) { return this.$val.Do(f); };
	ptrType$3.methods = [{prop: "Do", name: "Do", pkg: "", typ: $funcType([funcType], [], false)}];
	Once.init([{prop: "doing", name: "doing", pkg: "github.com/gopherjs/gopherjs/nosync", typ: $Bool, tag: ""}, {prop: "done", name: "done", pkg: "github.com/gopherjs/gopherjs/nosync", typ: $Bool, tag: ""}]);
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_nosync = function() { while (true) { switch ($s) { case 0:
		/* */ } return; } }; $init_nosync.$blocking = true; return $init_nosync;
	};
	return $pkg;
})();
$packages["bytes"] = (function() {
	var $pkg = {}, errors, io, unicode, utf8, IndexByte;
	errors = $packages["errors"];
	io = $packages["io"];
	unicode = $packages["unicode"];
	utf8 = $packages["unicode/utf8"];
	IndexByte = $pkg.IndexByte = function(s, c) {
		var _i, _ref, b, c, i, s;
		_ref = s;
		_i = 0;
		while (true) {
			if (!(_i < _ref.$length)) { break; }
			i = _i;
			b = ((_i < 0 || _i >= _ref.$length) ? $throwRuntimeError("index out of range") : _ref.$array[_ref.$offset + _i]);
			if (b === c) {
				return i;
			}
			_i++;
		}
		return -1;
	};
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_bytes = function() { while (true) { switch ($s) { case 0:
		$r = errors.$init($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		$r = io.$init($BLOCKING); /* */ $s = 2; case 2: if ($r && $r.$blocking) { $r = $r(); }
		$r = unicode.$init($BLOCKING); /* */ $s = 3; case 3: if ($r && $r.$blocking) { $r = $r(); }
		$r = utf8.$init($BLOCKING); /* */ $s = 4; case 4: if ($r && $r.$blocking) { $r = $r(); }
		$pkg.ErrTooLarge = errors.New("bytes.Buffer: too large");
		/* */ } return; } }; $init_bytes.$blocking = true; return $init_bytes;
	};
	return $pkg;
})();
$packages["syscall"] = (function() {
	var $pkg = {}, bytes, errors, js, runtime, sync, mmapper, Errno, sliceType, sliceType$1, sliceType$2, sliceType$3, arrayType$2, sliceType$36, sliceType$61, structType, ptrType$62, ptrType$63, sliceType$62, ptrType$64, ptrType$65, sliceType$70, ptrType$110, ptrType$111, mapType, funcType, funcType$1, warningPrinted, lineBuffer, syscallModule, alreadyTriedToLoad, minusOne, envOnce, envLock, env, envs, mapper, errors$1, init, printWarning, printToConsole, runtime_envs, syscall, Syscall, Syscall6, copyenv, Getenv, itoa, uitoa, mmap, munmap;
	bytes = $packages["bytes"];
	errors = $packages["errors"];
	js = $packages["github.com/gopherjs/gopherjs/js"];
	runtime = $packages["runtime"];
	sync = $packages["sync"];
	mmapper = $pkg.mmapper = $newType(0, $kindStruct, "syscall.mmapper", "mmapper", "syscall", function(Mutex_, active_, mmap_, munmap_) {
		this.$val = this;
		this.Mutex = Mutex_ !== undefined ? Mutex_ : new sync.Mutex.ptr();
		this.active = active_ !== undefined ? active_ : false;
		this.mmap = mmap_ !== undefined ? mmap_ : $throwNilPointerError;
		this.munmap = munmap_ !== undefined ? munmap_ : $throwNilPointerError;
	});
	Errno = $pkg.Errno = $newType(4, $kindUintptr, "syscall.Errno", "Errno", "syscall", null);
	sliceType = $sliceType($Uint8);
	sliceType$1 = $sliceType($String);
	sliceType$2 = $sliceType($String);
	sliceType$3 = $sliceType($Uint8);
	arrayType$2 = $arrayType($Uint8, 32);
	sliceType$36 = $sliceType($Uint8);
	sliceType$61 = $sliceType($Uint8);
	structType = $structType([{prop: "addr", name: "addr", pkg: "syscall", typ: $Uintptr, tag: ""}, {prop: "len", name: "len", pkg: "syscall", typ: $Int, tag: ""}, {prop: "cap", name: "cap", pkg: "syscall", typ: $Int, tag: ""}]);
	ptrType$62 = $ptrType($Uint8);
	ptrType$63 = $ptrType($Uint8);
	sliceType$62 = $sliceType($Uint8);
	ptrType$64 = $ptrType($Uint8);
	ptrType$65 = $ptrType($Uint8);
	sliceType$70 = $sliceType($Uint8);
	ptrType$110 = $ptrType(mmapper);
	ptrType$111 = $ptrType($Uint8);
	mapType = $mapType(ptrType$111, sliceType$62);
	funcType = $funcType([$Uintptr, $Uintptr, $Int, $Int, $Int, $Int64], [$Uintptr, $error], false);
	funcType$1 = $funcType([$Uintptr, $Uintptr], [$error], false);
	init = function() {
		$flushConsole = (function() {
			if (!((lineBuffer.$length === 0))) {
				$global.console.log($externalize($bytesToString(lineBuffer), $String));
				lineBuffer = sliceType.nil;
			}
		});
	};
	printWarning = function() {
		if (!warningPrinted) {
			console.log("warning: system calls not available, see https://github.com/gopherjs/gopherjs/blob/master/doc/syscalls.md");
		}
		warningPrinted = true;
	};
	printToConsole = function(b) {
		var b, goPrintToConsole, i;
		goPrintToConsole = $global.goPrintToConsole;
		if (!(goPrintToConsole === undefined)) {
			goPrintToConsole(b);
			return;
		}
		lineBuffer = $appendSlice(lineBuffer, b);
		while (true) {
			i = bytes.IndexByte(lineBuffer, 10);
			if (i === -1) {
				break;
			}
			$global.console.log($externalize($bytesToString($subslice(lineBuffer, 0, i)), $String));
			lineBuffer = $subslice(lineBuffer, (i + 1 >> 0));
		}
	};
	runtime_envs = function() {
		var envkeys, envs$1, i, jsEnv, key, process;
		process = $global.process;
		if (process === undefined) {
			return sliceType$1.nil;
		}
		jsEnv = process.env;
		envkeys = $global.Object.keys(jsEnv);
		envs$1 = $makeSlice(sliceType$2, $parseInt(envkeys.length));
		i = 0;
		while (true) {
			if (!(i < $parseInt(envkeys.length))) { break; }
			key = $internalize(envkeys[i], $String);
			((i < 0 || i >= envs$1.$length) ? $throwRuntimeError("index out of range") : envs$1.$array[envs$1.$offset + i] = key + "=" + $internalize(jsEnv[$externalize(key, $String)], $String));
			i = i + (1) >> 0;
		}
		return envs$1;
	};
	syscall = function(name) {
		var $deferred = [], $err = null, name, require;
		/* */ try { $deferFrames.push($deferred);
		$deferred.push([(function() {
			$recover();
		}), []]);
		if (syscallModule === null) {
			if (alreadyTriedToLoad) {
				return null;
			}
			alreadyTriedToLoad = true;
			require = $global.require;
			if (require === undefined) {
				$panic(new $String(""));
			}
			syscallModule = require($externalize("syscall", $String));
		}
		return syscallModule[$externalize(name, $String)];
		/* */ } catch(err) { $err = err; return null; } finally { $deferFrames.pop(); $callDeferred($deferred, $err); }
	};
	Syscall = $pkg.Syscall = function(trap, a1, a2, a3) {
		var _tmp, _tmp$1, _tmp$2, _tmp$3, _tmp$4, _tmp$5, _tmp$6, _tmp$7, _tmp$8, a1, a2, a3, array, err = 0, f, r, r1 = 0, r2 = 0, slice, trap;
		f = syscall("Syscall");
		if (!(f === null)) {
			r = f(trap, a1, a2, a3);
			_tmp = (($parseInt(r[0]) >> 0) >>> 0); _tmp$1 = (($parseInt(r[1]) >> 0) >>> 0); _tmp$2 = (($parseInt(r[2]) >> 0) >>> 0); r1 = _tmp; r2 = _tmp$1; err = _tmp$2;
			return [r1, r2, err];
		}
		if ((trap === 4) && ((a1 === 1) || (a1 === 2))) {
			array = a2;
			slice = $makeSlice(sliceType$3, $parseInt(array.length));
			slice.$array = array;
			printToConsole(slice);
			_tmp$3 = ($parseInt(array.length) >>> 0); _tmp$4 = 0; _tmp$5 = 0; r1 = _tmp$3; r2 = _tmp$4; err = _tmp$5;
			return [r1, r2, err];
		}
		printWarning();
		_tmp$6 = (minusOne >>> 0); _tmp$7 = 0; _tmp$8 = 13; r1 = _tmp$6; r2 = _tmp$7; err = _tmp$8;
		return [r1, r2, err];
	};
	Syscall6 = $pkg.Syscall6 = function(trap, a1, a2, a3, a4, a5, a6) {
		var _tmp, _tmp$1, _tmp$2, _tmp$3, _tmp$4, _tmp$5, a1, a2, a3, a4, a5, a6, err = 0, f, r, r1 = 0, r2 = 0, trap;
		f = syscall("Syscall6");
		if (!(f === null)) {
			r = f(trap, a1, a2, a3, a4, a5, a6);
			_tmp = (($parseInt(r[0]) >> 0) >>> 0); _tmp$1 = (($parseInt(r[1]) >> 0) >>> 0); _tmp$2 = (($parseInt(r[2]) >> 0) >>> 0); r1 = _tmp; r2 = _tmp$1; err = _tmp$2;
			return [r1, r2, err];
		}
		if (!((trap === 202))) {
			printWarning();
		}
		_tmp$3 = (minusOne >>> 0); _tmp$4 = 0; _tmp$5 = 13; r1 = _tmp$3; r2 = _tmp$4; err = _tmp$5;
		return [r1, r2, err];
	};
	copyenv = function() {
		var _entry, _i, _key, _ref, _tuple, i, j, key, ok, s;
		env = new $Map();
		_ref = envs;
		_i = 0;
		while (true) {
			if (!(_i < _ref.$length)) { break; }
			i = _i;
			s = ((_i < 0 || _i >= _ref.$length) ? $throwRuntimeError("index out of range") : _ref.$array[_ref.$offset + _i]);
			j = 0;
			while (true) {
				if (!(j < s.length)) { break; }
				if (s.charCodeAt(j) === 61) {
					key = s.substring(0, j);
					_tuple = (_entry = env[key], _entry !== undefined ? [_entry.v, true] : [0, false]); ok = _tuple[1];
					if (!ok) {
						_key = key; (env || $throwRuntimeError("assignment to entry in nil map"))[_key] = { k: _key, v: i };
					} else {
						((i < 0 || i >= envs.$length) ? $throwRuntimeError("index out of range") : envs.$array[envs.$offset + i] = "");
					}
					break;
				}
				j = j + (1) >> 0;
			}
			_i++;
		}
	};
	Getenv = $pkg.Getenv = function(key, $b) {
		var $args = arguments, $deferred = [], $err = null, $r, $s = 0, $this = this, _entry, _tmp, _tmp$1, _tmp$2, _tmp$3, _tmp$4, _tmp$5, _tmp$6, _tmp$7, _tuple, found = false, i, i$1, ok, s, value = "";
		/* */ if($b !== $BLOCKING) { $nonblockingCall(); }; var $blocking_Getenv = function() { try { $deferFrames.push($deferred); s: while (true) { switch ($s) { case 0:
		$r = envOnce.Do(copyenv, $BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		if (key.length === 0) {
			_tmp = ""; _tmp$1 = false; value = _tmp; found = _tmp$1;
			return [value, found];
		}
		$r = envLock.RLock($BLOCKING); /* */ $s = 2; case 2: if ($r && $r.$blocking) { $r = $r(); }
		$deferred.push([$methodVal(envLock, "RUnlock"), [$BLOCKING]]);
		_tuple = (_entry = env[key], _entry !== undefined ? [_entry.v, true] : [0, false]); i = _tuple[0]; ok = _tuple[1];
		if (!ok) {
			_tmp$2 = ""; _tmp$3 = false; value = _tmp$2; found = _tmp$3;
			return [value, found];
		}
		s = ((i < 0 || i >= envs.$length) ? $throwRuntimeError("index out of range") : envs.$array[envs.$offset + i]);
		i$1 = 0;
		while (true) {
			if (!(i$1 < s.length)) { break; }
			if (s.charCodeAt(i$1) === 61) {
				_tmp$4 = s.substring((i$1 + 1 >> 0)); _tmp$5 = true; value = _tmp$4; found = _tmp$5;
				return [value, found];
			}
			i$1 = i$1 + (1) >> 0;
		}
		_tmp$6 = ""; _tmp$7 = false; value = _tmp$6; found = _tmp$7;
		return [value, found];
		/* */ case -1: } return; } } catch(err) { $err = err; } finally { $deferFrames.pop(); if ($curGoroutine.asleep && !$jumpToDefer) { throw null; } $s = -1; $callDeferred($deferred, $err); return [value, found]; } }; $blocking_Getenv.$blocking = true; return $blocking_Getenv;
	};
	itoa = function(val) {
		var val;
		if (val < 0) {
			return "-" + uitoa((-val >>> 0));
		}
		return uitoa((val >>> 0));
	};
	uitoa = function(val) {
		var _q, _r, buf, i, val;
		buf = $clone(arrayType$2.zero(), arrayType$2);
		i = 31;
		while (true) {
			if (!(val >= 10)) { break; }
			((i < 0 || i >= buf.length) ? $throwRuntimeError("index out of range") : buf[i] = (((_r = val % 10, _r === _r ? _r : $throwRuntimeError("integer divide by zero")) + 48 >>> 0) << 24 >>> 24));
			i = i - (1) >> 0;
			val = (_q = val / (10), (_q === _q && _q !== 1/0 && _q !== -1/0) ? _q >>> 0 : $throwRuntimeError("integer divide by zero"));
		}
		((i < 0 || i >= buf.length) ? $throwRuntimeError("index out of range") : buf[i] = ((val + 48 >>> 0) << 24 >>> 24));
		return $bytesToString($subslice(new sliceType$36(buf), i));
	};
	mmapper.ptr.prototype.Mmap = function(fd, offset, length, prot, flags, $b) {
		var $args = arguments, $deferred = [], $err = null, $r, $s = 0, $this = this, _key, _tmp, _tmp$1, _tmp$2, _tmp$3, _tmp$4, _tmp$5, _tuple, addr, b, data = sliceType$61.nil, err = $ifaceNil, errno, m, p, sl, x, x$1;
		/* */ if($b !== $BLOCKING) { $nonblockingCall(); }; var $blocking_Mmap = function() { try { $deferFrames.push($deferred); s: while (true) { switch ($s) { case 0:
		m = $this;
		if (length <= 0) {
			_tmp = sliceType$61.nil; _tmp$1 = new Errno(22); data = _tmp; err = _tmp$1;
			return [data, err];
		}
		_tuple = m.mmap(0, (length >>> 0), prot, flags, fd, offset); addr = _tuple[0]; errno = _tuple[1];
		if (!($interfaceIsEqual(errno, $ifaceNil))) {
			_tmp$2 = sliceType$61.nil; _tmp$3 = errno; data = _tmp$2; err = _tmp$3;
			return [data, err];
		}
		sl = new structType.ptr(addr, length, length);
		b = sl;
		p = new ptrType$62(function() { return (x$1 = b.$capacity - 1 >> 0, ((x$1 < 0 || x$1 >= this.$target.$length) ? $throwRuntimeError("index out of range") : this.$target.$array[this.$target.$offset + x$1])); }, function($v) { (x = b.$capacity - 1 >> 0, ((x < 0 || x >= this.$target.$length) ? $throwRuntimeError("index out of range") : this.$target.$array[this.$target.$offset + x] = $v)); }, b);
		$r = m.Mutex.Lock($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		$deferred.push([$methodVal(m.Mutex, "Unlock"), [$BLOCKING]]);
		_key = p; (m.active || $throwRuntimeError("assignment to entry in nil map"))[_key.$key()] = { k: _key, v: b };
		_tmp$4 = b; _tmp$5 = $ifaceNil; data = _tmp$4; err = _tmp$5;
		return [data, err];
		/* */ case -1: } return; } } catch(err) { $err = err; } finally { $deferFrames.pop(); if ($curGoroutine.asleep && !$jumpToDefer) { throw null; } $s = -1; $callDeferred($deferred, $err); return [data, err]; } }; $blocking_Mmap.$blocking = true; return $blocking_Mmap;
	};
	mmapper.prototype.Mmap = function(fd, offset, length, prot, flags, $b) { return this.$val.Mmap(fd, offset, length, prot, flags, $b); };
	mmapper.ptr.prototype.Munmap = function(data, $b) {
		var $args = arguments, $deferred = [], $err = null, $r, $s = 0, $this = this, _entry, b, err = $ifaceNil, errno, m, p, x, x$1;
		/* */ if($b !== $BLOCKING) { $nonblockingCall(); }; var $blocking_Munmap = function() { try { $deferFrames.push($deferred); s: while (true) { switch ($s) { case 0:
		m = $this;
		if ((data.$length === 0) || !((data.$length === data.$capacity))) {
			err = new Errno(22);
			return err;
		}
		p = new ptrType$63(function() { return (x$1 = data.$capacity - 1 >> 0, ((x$1 < 0 || x$1 >= this.$target.$length) ? $throwRuntimeError("index out of range") : this.$target.$array[this.$target.$offset + x$1])); }, function($v) { (x = data.$capacity - 1 >> 0, ((x < 0 || x >= this.$target.$length) ? $throwRuntimeError("index out of range") : this.$target.$array[this.$target.$offset + x] = $v)); }, data);
		$r = m.Mutex.Lock($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		$deferred.push([$methodVal(m.Mutex, "Unlock"), [$BLOCKING]]);
		b = (_entry = m.active[p.$key()], _entry !== undefined ? _entry.v : sliceType$62.nil);
		if (b === sliceType$62.nil || !($pointerIsEqual(new ptrType$64(function() { return (0 >= this.$target.$length ? $throwRuntimeError("index out of range") : this.$target.$array[this.$target.$offset + 0]); }, function($v) { (0 >= this.$target.$length ? $throwRuntimeError("index out of range") : this.$target.$array[this.$target.$offset + 0] = $v); }, b), new ptrType$65(function() { return (0 >= this.$target.$length ? $throwRuntimeError("index out of range") : this.$target.$array[this.$target.$offset + 0]); }, function($v) { (0 >= this.$target.$length ? $throwRuntimeError("index out of range") : this.$target.$array[this.$target.$offset + 0] = $v); }, data)))) {
			err = new Errno(22);
			return err;
		}
		errno = m.munmap($sliceToArray(b), (b.$length >>> 0));
		if (!($interfaceIsEqual(errno, $ifaceNil))) {
			err = errno;
			return err;
		}
		delete m.active[p.$key()];
		err = $ifaceNil;
		return err;
		/* */ case -1: } return; } } catch(err) { $err = err; } finally { $deferFrames.pop(); if ($curGoroutine.asleep && !$jumpToDefer) { throw null; } $s = -1; $callDeferred($deferred, $err); return err; } }; $blocking_Munmap.$blocking = true; return $blocking_Munmap;
	};
	mmapper.prototype.Munmap = function(data, $b) { return this.$val.Munmap(data, $b); };
	Errno.prototype.Error = function() {
		var e, s;
		e = this.$val;
		if (0 <= (e >> 0) && (e >> 0) < 106) {
			s = ((e < 0 || e >= errors$1.length) ? $throwRuntimeError("index out of range") : errors$1[e]);
			if (!(s === "")) {
				return s;
			}
		}
		return "errno " + itoa((e >> 0));
	};
	$ptrType(Errno).prototype.Error = function() { return new Errno(this.$get()).Error(); };
	Errno.prototype.Temporary = function() {
		var e;
		e = this.$val;
		return (e === 4) || (e === 24) || (e === 54) || (e === 53) || new Errno(e).Timeout();
	};
	$ptrType(Errno).prototype.Temporary = function() { return new Errno(this.$get()).Temporary(); };
	Errno.prototype.Timeout = function() {
		var e;
		e = this.$val;
		return (e === 35) || (e === 35) || (e === 60);
	};
	$ptrType(Errno).prototype.Timeout = function() { return new Errno(this.$get()).Timeout(); };
	mmap = function(addr, length, prot, flag, fd, pos) {
		var _tuple, addr, e1, err = $ifaceNil, fd, flag, length, pos, prot, r0, ret = 0;
		_tuple = Syscall6(197, addr, length, (prot >>> 0), (flag >>> 0), (fd >>> 0), (pos.$low >>> 0)); r0 = _tuple[0]; e1 = _tuple[2];
		ret = r0;
		if (!((e1 === 0))) {
			err = new Errno(e1);
		}
		return [ret, err];
	};
	munmap = function(addr, length) {
		var _tuple, addr, e1, err = $ifaceNil, length;
		_tuple = Syscall(73, addr, length, 0); e1 = _tuple[2];
		if (!((e1 === 0))) {
			err = new Errno(e1);
		}
		return err;
	};
	ptrType$110.methods = [{prop: "Mmap", name: "Mmap", pkg: "", typ: $funcType([$Int, $Int64, $Int, $Int, $Int], [sliceType$61, $error], false)}, {prop: "Munmap", name: "Munmap", pkg: "", typ: $funcType([sliceType$70], [$error], false)}];
	Errno.methods = [{prop: "Error", name: "Error", pkg: "", typ: $funcType([], [$String], false)}, {prop: "Temporary", name: "Temporary", pkg: "", typ: $funcType([], [$Bool], false)}, {prop: "Timeout", name: "Timeout", pkg: "", typ: $funcType([], [$Bool], false)}];
	mmapper.init([{prop: "Mutex", name: "", pkg: "", typ: sync.Mutex, tag: ""}, {prop: "active", name: "active", pkg: "syscall", typ: mapType, tag: ""}, {prop: "mmap", name: "mmap", pkg: "syscall", typ: funcType, tag: ""}, {prop: "munmap", name: "munmap", pkg: "syscall", typ: funcType$1, tag: ""}]);
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_syscall = function() { while (true) { switch ($s) { case 0:
		$r = bytes.$init($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		$r = errors.$init($BLOCKING); /* */ $s = 2; case 2: if ($r && $r.$blocking) { $r = $r(); }
		$r = js.$init($BLOCKING); /* */ $s = 3; case 3: if ($r && $r.$blocking) { $r = $r(); }
		$r = runtime.$init($BLOCKING); /* */ $s = 4; case 4: if ($r && $r.$blocking) { $r = $r(); }
		$r = sync.$init($BLOCKING); /* */ $s = 5; case 5: if ($r && $r.$blocking) { $r = $r(); }
		lineBuffer = sliceType.nil;
		syscallModule = null;
		envOnce = new sync.Once.ptr();
		envLock = new sync.RWMutex.ptr();
		env = false;
		warningPrinted = false;
		alreadyTriedToLoad = false;
		minusOne = -1;
		envs = runtime_envs();
		errors$1 = $toNativeArray($kindString, ["", "operation not permitted", "no such file or directory", "no such process", "interrupted system call", "input/output error", "device not configured", "argument list too long", "exec format error", "bad file descriptor", "no child processes", "resource deadlock avoided", "cannot allocate memory", "permission denied", "bad address", "block device required", "resource busy", "file exists", "cross-device link", "operation not supported by device", "not a directory", "is a directory", "invalid argument", "too many open files in system", "too many open files", "inappropriate ioctl for device", "text file busy", "file too large", "no space left on device", "illegal seek", "read-only file system", "too many links", "broken pipe", "numerical argument out of domain", "result too large", "resource temporarily unavailable", "operation now in progress", "operation already in progress", "socket operation on non-socket", "destination address required", "message too long", "protocol wrong type for socket", "protocol not available", "protocol not supported", "socket type not supported", "operation not supported", "protocol family not supported", "address family not supported by protocol family", "address already in use", "can't assign requested address", "network is down", "network is unreachable", "network dropped connection on reset", "software caused connection abort", "connection reset by peer", "no buffer space available", "socket is already connected", "socket is not connected", "can't send after socket shutdown", "too many references: can't splice", "operation timed out", "connection refused", "too many levels of symbolic links", "file name too long", "host is down", "no route to host", "directory not empty", "too many processes", "too many users", "disc quota exceeded", "stale NFS file handle", "too many levels of remote in path", "RPC struct is bad", "RPC version wrong", "RPC prog. not avail", "program version wrong", "bad procedure for program", "no locks available", "function not implemented", "inappropriate file type or format", "authentication error", "need authenticator", "device power is off", "device error", "value too large to be stored in data type", "bad executable (or shared library)", "bad CPU type in executable", "shared library version mismatch", "malformed Mach-o file", "operation canceled", "identifier removed", "no message of desired type", "illegal byte sequence", "attribute not found", "bad message", "EMULTIHOP (Reserved)", "no message available on STREAM", "ENOLINK (Reserved)", "no STREAM resources", "not a STREAM", "protocol error", "STREAM ioctl timeout", "operation not supported on socket", "policy not found", "state not recoverable", "previous owner died"]);
		mapper = new mmapper.ptr(new sync.Mutex.ptr(), new $Map(), mmap, munmap);
		init();
		/* */ } return; } }; $init_syscall.$blocking = true; return $init_syscall;
	};
	return $pkg;
})();
$packages["time"] = (function() {
	var $pkg = {}, errors, js, nosync, runtime, strings, syscall, ParseError, Time, Month, Weekday, Duration, Location, zone, zoneTrans, sliceType, sliceType$1, sliceType$2, sliceType$3, sliceType$4, sliceType$5, ptrType, sliceType$6, sliceType$7, arrayType, sliceType$8, arrayType$1, sliceType$9, sliceType$10, arrayType$2, sliceType$11, sliceType$12, ptrType$1, ptrType$2, arrayType$4, sliceType$17, sliceType$18, sliceType$19, sliceType$20, sliceType$21, sliceType$22, sliceType$23, sliceType$24, ptrType$3, sliceType$25, sliceType$26, sliceType$27, sliceType$28, sliceType$29, sliceType$30, ptrType$4, ptrType$5, sliceType$31, sliceType$32, ptrType$11, ptrType$14, sliceType$43, sliceType$44, sliceType$45, sliceType$46, sliceType$47, ptrType$15, ptrType$16, ptrType$17, std0x, longDayNames, shortDayNames, shortMonthNames, longMonthNames, atoiError, errBad, errLeadingInt, months, days, daysBefore, utcLoc, localLoc, localOnce, zoneinfo, badData, zoneDirs, _tuple, _r, initLocal, runtimeNano, now, startsWithLowerCase, nextStdChunk, match, lookup, appendUint, atoi, formatNano, quote, isDigit, getnum, cutspace, skip, Parse, parse, parseTimeZone, parseGMT, parseNanoseconds, leadingInt, absWeekday, absClock, fmtFrac, fmtInt, absDate, Now, isLeap, norm, Date, div, FixedZone;
	errors = $packages["errors"];
	js = $packages["github.com/gopherjs/gopherjs/js"];
	nosync = $packages["github.com/gopherjs/gopherjs/nosync"];
	runtime = $packages["runtime"];
	strings = $packages["strings"];
	syscall = $packages["syscall"];
	ParseError = $pkg.ParseError = $newType(0, $kindStruct, "time.ParseError", "ParseError", "time", function(Layout_, Value_, LayoutElem_, ValueElem_, Message_) {
		this.$val = this;
		this.Layout = Layout_ !== undefined ? Layout_ : "";
		this.Value = Value_ !== undefined ? Value_ : "";
		this.LayoutElem = LayoutElem_ !== undefined ? LayoutElem_ : "";
		this.ValueElem = ValueElem_ !== undefined ? ValueElem_ : "";
		this.Message = Message_ !== undefined ? Message_ : "";
	});
	Time = $pkg.Time = $newType(0, $kindStruct, "time.Time", "Time", "time", function(sec_, nsec_, loc_) {
		this.$val = this;
		this.sec = sec_ !== undefined ? sec_ : new $Int64(0, 0);
		this.nsec = nsec_ !== undefined ? nsec_ : 0;
		this.loc = loc_ !== undefined ? loc_ : ptrType$2.nil;
	});
	Month = $pkg.Month = $newType(4, $kindInt, "time.Month", "Month", "time", null);
	Weekday = $pkg.Weekday = $newType(4, $kindInt, "time.Weekday", "Weekday", "time", null);
	Duration = $pkg.Duration = $newType(8, $kindInt64, "time.Duration", "Duration", "time", null);
	Location = $pkg.Location = $newType(0, $kindStruct, "time.Location", "Location", "time", function(name_, zone_, tx_, cacheStart_, cacheEnd_, cacheZone_) {
		this.$val = this;
		this.name = name_ !== undefined ? name_ : "";
		this.zone = zone_ !== undefined ? zone_ : sliceType$4.nil;
		this.tx = tx_ !== undefined ? tx_ : sliceType$5.nil;
		this.cacheStart = cacheStart_ !== undefined ? cacheStart_ : new $Int64(0, 0);
		this.cacheEnd = cacheEnd_ !== undefined ? cacheEnd_ : new $Int64(0, 0);
		this.cacheZone = cacheZone_ !== undefined ? cacheZone_ : ptrType.nil;
	});
	zone = $pkg.zone = $newType(0, $kindStruct, "time.zone", "zone", "time", function(name_, offset_, isDST_) {
		this.$val = this;
		this.name = name_ !== undefined ? name_ : "";
		this.offset = offset_ !== undefined ? offset_ : 0;
		this.isDST = isDST_ !== undefined ? isDST_ : false;
	});
	zoneTrans = $pkg.zoneTrans = $newType(0, $kindStruct, "time.zoneTrans", "zoneTrans", "time", function(when_, index_, isstd_, isutc_) {
		this.$val = this;
		this.when = when_ !== undefined ? when_ : new $Int64(0, 0);
		this.index = index_ !== undefined ? index_ : 0;
		this.isstd = isstd_ !== undefined ? isstd_ : false;
		this.isutc = isutc_ !== undefined ? isutc_ : false;
	});
	sliceType = $sliceType($String);
	sliceType$1 = $sliceType($String);
	sliceType$2 = $sliceType($String);
	sliceType$3 = $sliceType($String);
	sliceType$4 = $sliceType(zone);
	sliceType$5 = $sliceType(zoneTrans);
	ptrType = $ptrType(zone);
	sliceType$6 = $sliceType($String);
	sliceType$7 = $sliceType(zone);
	arrayType = $arrayType($Uint8, 32);
	sliceType$8 = $sliceType($Uint8);
	arrayType$1 = $arrayType($Uint8, 9);
	sliceType$9 = $sliceType($Uint8);
	sliceType$10 = $sliceType($Uint8);
	arrayType$2 = $arrayType($Uint8, 64);
	sliceType$11 = $sliceType($Uint8);
	sliceType$12 = $sliceType($Uint8);
	ptrType$1 = $ptrType(Location);
	ptrType$2 = $ptrType(Location);
	arrayType$4 = $arrayType($Uint8, 32);
	sliceType$17 = $sliceType($Uint8);
	sliceType$18 = $sliceType($Uint8);
	sliceType$19 = $sliceType($Uint8);
	sliceType$20 = $sliceType($Uint8);
	sliceType$21 = $sliceType($Uint8);
	sliceType$22 = $sliceType($Uint8);
	sliceType$23 = $sliceType($Uint8);
	sliceType$24 = $sliceType($Uint8);
	ptrType$3 = $ptrType(Location);
	sliceType$25 = $sliceType($Uint8);
	sliceType$26 = $sliceType($Uint8);
	sliceType$27 = $sliceType($Uint8);
	sliceType$28 = $sliceType($Uint8);
	sliceType$29 = $sliceType($Uint8);
	sliceType$30 = $sliceType($Uint8);
	ptrType$4 = $ptrType(Location);
	ptrType$5 = $ptrType(Location);
	sliceType$31 = $sliceType(zone);
	sliceType$32 = $sliceType(zoneTrans);
	ptrType$11 = $ptrType(ParseError);
	ptrType$14 = $ptrType(Location);
	sliceType$43 = $sliceType($Uint8);
	sliceType$44 = $sliceType($Uint8);
	sliceType$45 = $sliceType($Uint8);
	sliceType$46 = $sliceType($Uint8);
	sliceType$47 = $sliceType($Uint8);
	ptrType$15 = $ptrType(Time);
	ptrType$16 = $ptrType(Location);
	ptrType$17 = $ptrType(Location);
	initLocal = function() {
		var d, i, j, s;
		d = new ($global.Date)();
		s = $internalize(d, $String);
		i = strings.IndexByte(s, 40);
		j = strings.IndexByte(s, 41);
		if ((i === -1) || (j === -1)) {
			localLoc.name = "UTC";
			return;
		}
		localLoc.name = s.substring((i + 1 >> 0), j);
		localLoc.zone = new sliceType$7([new zone.ptr(localLoc.name, ($parseInt(d.getTimezoneOffset()) >> 0) * -60 >> 0, false)]);
	};
	runtimeNano = function() {
		return $mul64($internalize(new ($global.Date)().getTime(), $Int64), new $Int64(0, 1000000));
	};
	now = function() {
		var _tmp, _tmp$1, n, nsec = 0, sec = new $Int64(0, 0), x;
		n = runtimeNano();
		_tmp = $div64(n, new $Int64(0, 1000000000), false); _tmp$1 = ((x = $div64(n, new $Int64(0, 1000000000), true), x.$low + ((x.$high >> 31) * 4294967296)) >> 0); sec = _tmp; nsec = _tmp$1;
		return [sec, nsec];
	};
	startsWithLowerCase = function(str) {
		var c, str;
		if (str.length === 0) {
			return false;
		}
		c = str.charCodeAt(0);
		return 97 <= c && c <= 122;
	};
	nextStdChunk = function(layout) {
		var _ref, _tmp, _tmp$1, _tmp$10, _tmp$11, _tmp$12, _tmp$13, _tmp$14, _tmp$15, _tmp$16, _tmp$17, _tmp$18, _tmp$19, _tmp$2, _tmp$20, _tmp$21, _tmp$22, _tmp$23, _tmp$24, _tmp$25, _tmp$26, _tmp$27, _tmp$28, _tmp$29, _tmp$3, _tmp$30, _tmp$31, _tmp$32, _tmp$33, _tmp$34, _tmp$35, _tmp$36, _tmp$37, _tmp$38, _tmp$39, _tmp$4, _tmp$40, _tmp$41, _tmp$42, _tmp$43, _tmp$44, _tmp$45, _tmp$46, _tmp$47, _tmp$48, _tmp$49, _tmp$5, _tmp$50, _tmp$51, _tmp$52, _tmp$53, _tmp$54, _tmp$55, _tmp$56, _tmp$57, _tmp$58, _tmp$59, _tmp$6, _tmp$60, _tmp$61, _tmp$62, _tmp$63, _tmp$64, _tmp$65, _tmp$66, _tmp$67, _tmp$68, _tmp$69, _tmp$7, _tmp$70, _tmp$71, _tmp$72, _tmp$73, _tmp$74, _tmp$75, _tmp$76, _tmp$77, _tmp$78, _tmp$79, _tmp$8, _tmp$80, _tmp$9, c, ch, i, j, layout, prefix = "", std = 0, std$1, suffix = "", x;
		i = 0;
		while (true) {
			if (!(i < layout.length)) { break; }
			c = (layout.charCodeAt(i) >> 0);
			_ref = c;
			if (_ref === 74) {
				if (layout.length >= (i + 3 >> 0) && layout.substring(i, (i + 3 >> 0)) === "Jan") {
					if (layout.length >= (i + 7 >> 0) && layout.substring(i, (i + 7 >> 0)) === "January") {
						_tmp = layout.substring(0, i); _tmp$1 = 257; _tmp$2 = layout.substring((i + 7 >> 0)); prefix = _tmp; std = _tmp$1; suffix = _tmp$2;
						return [prefix, std, suffix];
					}
					if (!startsWithLowerCase(layout.substring((i + 3 >> 0)))) {
						_tmp$3 = layout.substring(0, i); _tmp$4 = 258; _tmp$5 = layout.substring((i + 3 >> 0)); prefix = _tmp$3; std = _tmp$4; suffix = _tmp$5;
						return [prefix, std, suffix];
					}
				}
			} else if (_ref === 77) {
				if (layout.length >= (i + 3 >> 0)) {
					if (layout.substring(i, (i + 3 >> 0)) === "Mon") {
						if (layout.length >= (i + 6 >> 0) && layout.substring(i, (i + 6 >> 0)) === "Monday") {
							_tmp$6 = layout.substring(0, i); _tmp$7 = 261; _tmp$8 = layout.substring((i + 6 >> 0)); prefix = _tmp$6; std = _tmp$7; suffix = _tmp$8;
							return [prefix, std, suffix];
						}
						if (!startsWithLowerCase(layout.substring((i + 3 >> 0)))) {
							_tmp$9 = layout.substring(0, i); _tmp$10 = 262; _tmp$11 = layout.substring((i + 3 >> 0)); prefix = _tmp$9; std = _tmp$10; suffix = _tmp$11;
							return [prefix, std, suffix];
						}
					}
					if (layout.substring(i, (i + 3 >> 0)) === "MST") {
						_tmp$12 = layout.substring(0, i); _tmp$13 = 21; _tmp$14 = layout.substring((i + 3 >> 0)); prefix = _tmp$12; std = _tmp$13; suffix = _tmp$14;
						return [prefix, std, suffix];
					}
				}
			} else if (_ref === 48) {
				if (layout.length >= (i + 2 >> 0) && 49 <= layout.charCodeAt((i + 1 >> 0)) && layout.charCodeAt((i + 1 >> 0)) <= 54) {
					_tmp$15 = layout.substring(0, i); _tmp$16 = (x = layout.charCodeAt((i + 1 >> 0)) - 49 << 24 >>> 24, ((x < 0 || x >= std0x.length) ? $throwRuntimeError("index out of range") : std0x[x])); _tmp$17 = layout.substring((i + 2 >> 0)); prefix = _tmp$15; std = _tmp$16; suffix = _tmp$17;
					return [prefix, std, suffix];
				}
			} else if (_ref === 49) {
				if (layout.length >= (i + 2 >> 0) && (layout.charCodeAt((i + 1 >> 0)) === 53)) {
					_tmp$18 = layout.substring(0, i); _tmp$19 = 522; _tmp$20 = layout.substring((i + 2 >> 0)); prefix = _tmp$18; std = _tmp$19; suffix = _tmp$20;
					return [prefix, std, suffix];
				}
				_tmp$21 = layout.substring(0, i); _tmp$22 = 259; _tmp$23 = layout.substring((i + 1 >> 0)); prefix = _tmp$21; std = _tmp$22; suffix = _tmp$23;
				return [prefix, std, suffix];
			} else if (_ref === 50) {
				if (layout.length >= (i + 4 >> 0) && layout.substring(i, (i + 4 >> 0)) === "2006") {
					_tmp$24 = layout.substring(0, i); _tmp$25 = 273; _tmp$26 = layout.substring((i + 4 >> 0)); prefix = _tmp$24; std = _tmp$25; suffix = _tmp$26;
					return [prefix, std, suffix];
				}
				_tmp$27 = layout.substring(0, i); _tmp$28 = 263; _tmp$29 = layout.substring((i + 1 >> 0)); prefix = _tmp$27; std = _tmp$28; suffix = _tmp$29;
				return [prefix, std, suffix];
			} else if (_ref === 95) {
				if (layout.length >= (i + 2 >> 0) && (layout.charCodeAt((i + 1 >> 0)) === 50)) {
					_tmp$30 = layout.substring(0, i); _tmp$31 = 264; _tmp$32 = layout.substring((i + 2 >> 0)); prefix = _tmp$30; std = _tmp$31; suffix = _tmp$32;
					return [prefix, std, suffix];
				}
			} else if (_ref === 51) {
				_tmp$33 = layout.substring(0, i); _tmp$34 = 523; _tmp$35 = layout.substring((i + 1 >> 0)); prefix = _tmp$33; std = _tmp$34; suffix = _tmp$35;
				return [prefix, std, suffix];
			} else if (_ref === 52) {
				_tmp$36 = layout.substring(0, i); _tmp$37 = 525; _tmp$38 = layout.substring((i + 1 >> 0)); prefix = _tmp$36; std = _tmp$37; suffix = _tmp$38;
				return [prefix, std, suffix];
			} else if (_ref === 53) {
				_tmp$39 = layout.substring(0, i); _tmp$40 = 527; _tmp$41 = layout.substring((i + 1 >> 0)); prefix = _tmp$39; std = _tmp$40; suffix = _tmp$41;
				return [prefix, std, suffix];
			} else if (_ref === 80) {
				if (layout.length >= (i + 2 >> 0) && (layout.charCodeAt((i + 1 >> 0)) === 77)) {
					_tmp$42 = layout.substring(0, i); _tmp$43 = 531; _tmp$44 = layout.substring((i + 2 >> 0)); prefix = _tmp$42; std = _tmp$43; suffix = _tmp$44;
					return [prefix, std, suffix];
				}
			} else if (_ref === 112) {
				if (layout.length >= (i + 2 >> 0) && (layout.charCodeAt((i + 1 >> 0)) === 109)) {
					_tmp$45 = layout.substring(0, i); _tmp$46 = 532; _tmp$47 = layout.substring((i + 2 >> 0)); prefix = _tmp$45; std = _tmp$46; suffix = _tmp$47;
					return [prefix, std, suffix];
				}
			} else if (_ref === 45) {
				if (layout.length >= (i + 7 >> 0) && layout.substring(i, (i + 7 >> 0)) === "-070000") {
					_tmp$48 = layout.substring(0, i); _tmp$49 = 27; _tmp$50 = layout.substring((i + 7 >> 0)); prefix = _tmp$48; std = _tmp$49; suffix = _tmp$50;
					return [prefix, std, suffix];
				}
				if (layout.length >= (i + 9 >> 0) && layout.substring(i, (i + 9 >> 0)) === "-07:00:00") {
					_tmp$51 = layout.substring(0, i); _tmp$52 = 30; _tmp$53 = layout.substring((i + 9 >> 0)); prefix = _tmp$51; std = _tmp$52; suffix = _tmp$53;
					return [prefix, std, suffix];
				}
				if (layout.length >= (i + 5 >> 0) && layout.substring(i, (i + 5 >> 0)) === "-0700") {
					_tmp$54 = layout.substring(0, i); _tmp$55 = 26; _tmp$56 = layout.substring((i + 5 >> 0)); prefix = _tmp$54; std = _tmp$55; suffix = _tmp$56;
					return [prefix, std, suffix];
				}
				if (layout.length >= (i + 6 >> 0) && layout.substring(i, (i + 6 >> 0)) === "-07:00") {
					_tmp$57 = layout.substring(0, i); _tmp$58 = 29; _tmp$59 = layout.substring((i + 6 >> 0)); prefix = _tmp$57; std = _tmp$58; suffix = _tmp$59;
					return [prefix, std, suffix];
				}
				if (layout.length >= (i + 3 >> 0) && layout.substring(i, (i + 3 >> 0)) === "-07") {
					_tmp$60 = layout.substring(0, i); _tmp$61 = 28; _tmp$62 = layout.substring((i + 3 >> 0)); prefix = _tmp$60; std = _tmp$61; suffix = _tmp$62;
					return [prefix, std, suffix];
				}
			} else if (_ref === 90) {
				if (layout.length >= (i + 7 >> 0) && layout.substring(i, (i + 7 >> 0)) === "Z070000") {
					_tmp$63 = layout.substring(0, i); _tmp$64 = 23; _tmp$65 = layout.substring((i + 7 >> 0)); prefix = _tmp$63; std = _tmp$64; suffix = _tmp$65;
					return [prefix, std, suffix];
				}
				if (layout.length >= (i + 9 >> 0) && layout.substring(i, (i + 9 >> 0)) === "Z07:00:00") {
					_tmp$66 = layout.substring(0, i); _tmp$67 = 25; _tmp$68 = layout.substring((i + 9 >> 0)); prefix = _tmp$66; std = _tmp$67; suffix = _tmp$68;
					return [prefix, std, suffix];
				}
				if (layout.length >= (i + 5 >> 0) && layout.substring(i, (i + 5 >> 0)) === "Z0700") {
					_tmp$69 = layout.substring(0, i); _tmp$70 = 22; _tmp$71 = layout.substring((i + 5 >> 0)); prefix = _tmp$69; std = _tmp$70; suffix = _tmp$71;
					return [prefix, std, suffix];
				}
				if (layout.length >= (i + 6 >> 0) && layout.substring(i, (i + 6 >> 0)) === "Z07:00") {
					_tmp$72 = layout.substring(0, i); _tmp$73 = 24; _tmp$74 = layout.substring((i + 6 >> 0)); prefix = _tmp$72; std = _tmp$73; suffix = _tmp$74;
					return [prefix, std, suffix];
				}
			} else if (_ref === 46) {
				if ((i + 1 >> 0) < layout.length && ((layout.charCodeAt((i + 1 >> 0)) === 48) || (layout.charCodeAt((i + 1 >> 0)) === 57))) {
					ch = layout.charCodeAt((i + 1 >> 0));
					j = i + 1 >> 0;
					while (true) {
						if (!(j < layout.length && (layout.charCodeAt(j) === ch))) { break; }
						j = j + (1) >> 0;
					}
					if (!isDigit(layout, j)) {
						std$1 = 31;
						if (layout.charCodeAt((i + 1 >> 0)) === 57) {
							std$1 = 32;
						}
						std$1 = std$1 | ((((j - ((i + 1 >> 0)) >> 0)) << 16 >> 0));
						_tmp$75 = layout.substring(0, i); _tmp$76 = std$1; _tmp$77 = layout.substring(j); prefix = _tmp$75; std = _tmp$76; suffix = _tmp$77;
						return [prefix, std, suffix];
					}
				}
			}
			i = i + (1) >> 0;
		}
		_tmp$78 = layout; _tmp$79 = 0; _tmp$80 = ""; prefix = _tmp$78; std = _tmp$79; suffix = _tmp$80;
		return [prefix, std, suffix];
	};
	match = function(s1, s2) {
		var c1, c2, i, s1, s2;
		i = 0;
		while (true) {
			if (!(i < s1.length)) { break; }
			c1 = s1.charCodeAt(i);
			c2 = s2.charCodeAt(i);
			if (!((c1 === c2))) {
				c1 = (c1 | (32)) >>> 0;
				c2 = (c2 | (32)) >>> 0;
				if (!((c1 === c2)) || c1 < 97 || c1 > 122) {
					return false;
				}
			}
			i = i + (1) >> 0;
		}
		return true;
	};
	lookup = function(tab, val) {
		var _i, _ref, i, tab, v, val;
		_ref = tab;
		_i = 0;
		while (true) {
			if (!(_i < _ref.$length)) { break; }
			i = _i;
			v = ((_i < 0 || _i >= _ref.$length) ? $throwRuntimeError("index out of range") : _ref.$array[_ref.$offset + _i]);
			if (val.length >= v.length && match(val.substring(0, v.length), v)) {
				return [i, val.substring(v.length), $ifaceNil];
			}
			_i++;
		}
		return [-1, val, errBad];
	};
	appendUint = function(b, x, pad) {
		var _q, _q$1, _r$1, _r$2, b, buf, n, pad, x;
		if (x < 10) {
			if (!((pad === 0))) {
				b = $append(b, pad);
			}
			return $append(b, ((48 + x >>> 0) << 24 >>> 24));
		}
		if (x < 100) {
			b = $append(b, ((48 + (_q = x / 10, (_q === _q && _q !== 1/0 && _q !== -1/0) ? _q >>> 0 : $throwRuntimeError("integer divide by zero")) >>> 0) << 24 >>> 24));
			b = $append(b, ((48 + (_r$1 = x % 10, _r$1 === _r$1 ? _r$1 : $throwRuntimeError("integer divide by zero")) >>> 0) << 24 >>> 24));
			return b;
		}
		buf = $clone(arrayType.zero(), arrayType);
		n = 32;
		if (x === 0) {
			return $append(b, 48);
		}
		while (true) {
			if (!(x >= 10)) { break; }
			n = n - (1) >> 0;
			((n < 0 || n >= buf.length) ? $throwRuntimeError("index out of range") : buf[n] = (((_r$2 = x % 10, _r$2 === _r$2 ? _r$2 : $throwRuntimeError("integer divide by zero")) + 48 >>> 0) << 24 >>> 24));
			x = (_q$1 = x / (10), (_q$1 === _q$1 && _q$1 !== 1/0 && _q$1 !== -1/0) ? _q$1 >>> 0 : $throwRuntimeError("integer divide by zero"));
		}
		n = n - (1) >> 0;
		((n < 0 || n >= buf.length) ? $throwRuntimeError("index out of range") : buf[n] = ((x + 48 >>> 0) << 24 >>> 24));
		return $appendSlice(b, $subslice(new sliceType$8(buf), n));
	};
	atoi = function(s) {
		var _tmp, _tmp$1, _tmp$2, _tmp$3, _tuple$1, err = $ifaceNil, neg, q, rem, s, x = 0;
		neg = false;
		if (!(s === "") && ((s.charCodeAt(0) === 45) || (s.charCodeAt(0) === 43))) {
			neg = s.charCodeAt(0) === 45;
			s = s.substring(1);
		}
		_tuple$1 = leadingInt(s); q = _tuple$1[0]; rem = _tuple$1[1]; err = _tuple$1[2];
		x = ((q.$low + ((q.$high >> 31) * 4294967296)) >> 0);
		if (!($interfaceIsEqual(err, $ifaceNil)) || !(rem === "")) {
			_tmp = 0; _tmp$1 = atoiError; x = _tmp; err = _tmp$1;
			return [x, err];
		}
		if (neg) {
			x = -x;
		}
		_tmp$2 = x; _tmp$3 = $ifaceNil; x = _tmp$2; err = _tmp$3;
		return [x, err];
	};
	formatNano = function(b, nanosec, n, trim) {
		var _q, _r$1, b, buf, n, nanosec, start, trim, u, x;
		u = nanosec;
		buf = $clone(arrayType$1.zero(), arrayType$1);
		start = 9;
		while (true) {
			if (!(start > 0)) { break; }
			start = start - (1) >> 0;
			((start < 0 || start >= buf.length) ? $throwRuntimeError("index out of range") : buf[start] = (((_r$1 = u % 10, _r$1 === _r$1 ? _r$1 : $throwRuntimeError("integer divide by zero")) + 48 >>> 0) << 24 >>> 24));
			u = (_q = u / (10), (_q === _q && _q !== 1/0 && _q !== -1/0) ? _q >>> 0 : $throwRuntimeError("integer divide by zero"));
		}
		if (n > 9) {
			n = 9;
		}
		if (trim) {
			while (true) {
				if (!(n > 0 && ((x = n - 1 >> 0, ((x < 0 || x >= buf.length) ? $throwRuntimeError("index out of range") : buf[x])) === 48))) { break; }
				n = n - (1) >> 0;
			}
			if (n === 0) {
				return b;
			}
		}
		b = $append(b, 46);
		return $appendSlice(b, $subslice(new sliceType$9(buf), 0, n));
	};
	Time.ptr.prototype.String = function() {
		var t;
		t = $clone(this, Time);
		return t.Format("2006-01-02 15:04:05.999999999 -0700 MST");
	};
	Time.prototype.String = function() { return this.$val.String(); };
	Time.ptr.prototype.Format = function(layout) {
		var _q, _q$1, _q$2, _q$3, _r$1, _r$2, _r$3, _r$4, _r$5, _r$6, _ref, _tuple$1, _tuple$2, _tuple$3, _tuple$4, abs, absoffset, b, buf, day, hour, hr, hr$1, layout, m, max, min, month, name, offset, prefix, s, sec, std, suffix, t, y, y$1, year, zone$1, zone$2;
		t = $clone(this, Time);
		_tuple$1 = t.locabs(); name = _tuple$1[0]; offset = _tuple$1[1]; abs = _tuple$1[2];
		year = -1;
		month = 0;
		day = 0;
		hour = -1;
		min = 0;
		sec = 0;
		b = sliceType$10.nil;
		buf = $clone(arrayType$2.zero(), arrayType$2);
		max = layout.length + 10 >> 0;
		if (max <= 64) {
			b = $subslice(new sliceType$11(buf), 0, 0);
		} else {
			b = $makeSlice(sliceType$12, 0, max);
		}
		while (true) {
			if (!(!(layout === ""))) { break; }
			_tuple$2 = nextStdChunk(layout); prefix = _tuple$2[0]; std = _tuple$2[1]; suffix = _tuple$2[2];
			if (!(prefix === "")) {
				b = $appendSlice(b, new sliceType$10($stringToBytes(prefix)));
			}
			if (std === 0) {
				break;
			}
			layout = suffix;
			if (year < 0 && !(((std & 256) === 0))) {
				_tuple$3 = absDate(abs, true); year = _tuple$3[0]; month = _tuple$3[1]; day = _tuple$3[2];
			}
			if (hour < 0 && !(((std & 512) === 0))) {
				_tuple$4 = absClock(abs); hour = _tuple$4[0]; min = _tuple$4[1]; sec = _tuple$4[2];
			}
			_ref = std & 65535;
			switch (0) { default: if (_ref === 274) {
				y = year;
				if (y < 0) {
					y = -y;
				}
				b = appendUint(b, ((_r$1 = y % 100, _r$1 === _r$1 ? _r$1 : $throwRuntimeError("integer divide by zero")) >>> 0), 48);
			} else if (_ref === 273) {
				y$1 = year;
				if (year <= -1000) {
					b = $append(b, 45);
					y$1 = -y$1;
				} else if (year <= -100) {
					b = $appendSlice(b, new sliceType$10($stringToBytes("-0")));
					y$1 = -y$1;
				} else if (year <= -10) {
					b = $appendSlice(b, new sliceType$10($stringToBytes("-00")));
					y$1 = -y$1;
				} else if (year < 0) {
					b = $appendSlice(b, new sliceType$10($stringToBytes("-000")));
					y$1 = -y$1;
				} else if (year < 10) {
					b = $appendSlice(b, new sliceType$10($stringToBytes("000")));
				} else if (year < 100) {
					b = $appendSlice(b, new sliceType$10($stringToBytes("00")));
				} else if (year < 1000) {
					b = $append(b, 48);
				}
				b = appendUint(b, (y$1 >>> 0), 0);
			} else if (_ref === 258) {
				b = $appendSlice(b, new sliceType$10($stringToBytes(new Month(month).String().substring(0, 3))));
			} else if (_ref === 257) {
				m = new Month(month).String();
				b = $appendSlice(b, new sliceType$10($stringToBytes(m)));
			} else if (_ref === 259) {
				b = appendUint(b, (month >>> 0), 0);
			} else if (_ref === 260) {
				b = appendUint(b, (month >>> 0), 48);
			} else if (_ref === 262) {
				b = $appendSlice(b, new sliceType$10($stringToBytes(new Weekday(absWeekday(abs)).String().substring(0, 3))));
			} else if (_ref === 261) {
				s = new Weekday(absWeekday(abs)).String();
				b = $appendSlice(b, new sliceType$10($stringToBytes(s)));
			} else if (_ref === 263) {
				b = appendUint(b, (day >>> 0), 0);
			} else if (_ref === 264) {
				b = appendUint(b, (day >>> 0), 32);
			} else if (_ref === 265) {
				b = appendUint(b, (day >>> 0), 48);
			} else if (_ref === 522) {
				b = appendUint(b, (hour >>> 0), 48);
			} else if (_ref === 523) {
				hr = (_r$2 = hour % 12, _r$2 === _r$2 ? _r$2 : $throwRuntimeError("integer divide by zero"));
				if (hr === 0) {
					hr = 12;
				}
				b = appendUint(b, (hr >>> 0), 0);
			} else if (_ref === 524) {
				hr$1 = (_r$3 = hour % 12, _r$3 === _r$3 ? _r$3 : $throwRuntimeError("integer divide by zero"));
				if (hr$1 === 0) {
					hr$1 = 12;
				}
				b = appendUint(b, (hr$1 >>> 0), 48);
			} else if (_ref === 525) {
				b = appendUint(b, (min >>> 0), 0);
			} else if (_ref === 526) {
				b = appendUint(b, (min >>> 0), 48);
			} else if (_ref === 527) {
				b = appendUint(b, (sec >>> 0), 0);
			} else if (_ref === 528) {
				b = appendUint(b, (sec >>> 0), 48);
			} else if (_ref === 531) {
				if (hour >= 12) {
					b = $appendSlice(b, new sliceType$10($stringToBytes("PM")));
				} else {
					b = $appendSlice(b, new sliceType$10($stringToBytes("AM")));
				}
			} else if (_ref === 532) {
				if (hour >= 12) {
					b = $appendSlice(b, new sliceType$10($stringToBytes("pm")));
				} else {
					b = $appendSlice(b, new sliceType$10($stringToBytes("am")));
				}
			} else if (_ref === 22 || _ref === 24 || _ref === 23 || _ref === 25 || _ref === 26 || _ref === 29 || _ref === 27 || _ref === 30) {
				if ((offset === 0) && ((std === 22) || (std === 24) || (std === 23) || (std === 25))) {
					b = $append(b, 90);
					break;
				}
				zone$1 = (_q = offset / 60, (_q === _q && _q !== 1/0 && _q !== -1/0) ? _q >> 0 : $throwRuntimeError("integer divide by zero"));
				absoffset = offset;
				if (zone$1 < 0) {
					b = $append(b, 45);
					zone$1 = -zone$1;
					absoffset = -absoffset;
				} else {
					b = $append(b, 43);
				}
				b = appendUint(b, ((_q$1 = zone$1 / 60, (_q$1 === _q$1 && _q$1 !== 1/0 && _q$1 !== -1/0) ? _q$1 >> 0 : $throwRuntimeError("integer divide by zero")) >>> 0), 48);
				if ((std === 24) || (std === 29) || (std === 25) || (std === 30)) {
					b = $append(b, 58);
				}
				b = appendUint(b, ((_r$4 = zone$1 % 60, _r$4 === _r$4 ? _r$4 : $throwRuntimeError("integer divide by zero")) >>> 0), 48);
				if ((std === 23) || (std === 27) || (std === 30) || (std === 25)) {
					if ((std === 30) || (std === 25)) {
						b = $append(b, 58);
					}
					b = appendUint(b, ((_r$5 = absoffset % 60, _r$5 === _r$5 ? _r$5 : $throwRuntimeError("integer divide by zero")) >>> 0), 48);
				}
			} else if (_ref === 21) {
				if (!(name === "")) {
					b = $appendSlice(b, new sliceType$10($stringToBytes(name)));
					break;
				}
				zone$2 = (_q$2 = offset / 60, (_q$2 === _q$2 && _q$2 !== 1/0 && _q$2 !== -1/0) ? _q$2 >> 0 : $throwRuntimeError("integer divide by zero"));
				if (zone$2 < 0) {
					b = $append(b, 45);
					zone$2 = -zone$2;
				} else {
					b = $append(b, 43);
				}
				b = appendUint(b, ((_q$3 = zone$2 / 60, (_q$3 === _q$3 && _q$3 !== 1/0 && _q$3 !== -1/0) ? _q$3 >> 0 : $throwRuntimeError("integer divide by zero")) >>> 0), 48);
				b = appendUint(b, ((_r$6 = zone$2 % 60, _r$6 === _r$6 ? _r$6 : $throwRuntimeError("integer divide by zero")) >>> 0), 48);
			} else if (_ref === 31 || _ref === 32) {
				b = formatNano(b, (t.Nanosecond() >>> 0), std >> 16 >> 0, (std & 65535) === 32);
			} }
		}
		return $bytesToString(b);
	};
	Time.prototype.Format = function(layout) { return this.$val.Format(layout); };
	quote = function(s) {
		var s;
		return "\"" + s + "\"";
	};
	ParseError.ptr.prototype.Error = function() {
		var e;
		e = this;
		if (e.Message === "") {
			return "parsing time " + quote(e.Value) + " as " + quote(e.Layout) + ": cannot parse " + quote(e.ValueElem) + " as " + quote(e.LayoutElem);
		}
		return "parsing time " + quote(e.Value) + e.Message;
	};
	ParseError.prototype.Error = function() { return this.$val.Error(); };
	isDigit = function(s, i) {
		var c, i, s;
		if (s.length <= i) {
			return false;
		}
		c = s.charCodeAt(i);
		return 48 <= c && c <= 57;
	};
	getnum = function(s, fixed) {
		var fixed, s;
		if (!isDigit(s, 0)) {
			return [0, s, errBad];
		}
		if (!isDigit(s, 1)) {
			if (fixed) {
				return [0, s, errBad];
			}
			return [((s.charCodeAt(0) - 48 << 24 >>> 24) >> 0), s.substring(1), $ifaceNil];
		}
		return [(((s.charCodeAt(0) - 48 << 24 >>> 24) >> 0) * 10 >> 0) + ((s.charCodeAt(1) - 48 << 24 >>> 24) >> 0) >> 0, s.substring(2), $ifaceNil];
	};
	cutspace = function(s) {
		var s;
		while (true) {
			if (!(s.length > 0 && (s.charCodeAt(0) === 32))) { break; }
			s = s.substring(1);
		}
		return s;
	};
	skip = function(value, prefix) {
		var prefix, value;
		while (true) {
			if (!(prefix.length > 0)) { break; }
			if (prefix.charCodeAt(0) === 32) {
				if (value.length > 0 && !((value.charCodeAt(0) === 32))) {
					return [value, errBad];
				}
				prefix = cutspace(prefix);
				value = cutspace(value);
				continue;
			}
			if ((value.length === 0) || !((value.charCodeAt(0) === prefix.charCodeAt(0)))) {
				return [value, errBad];
			}
			prefix = prefix.substring(1);
			value = value.substring(1);
		}
		return [value, $ifaceNil];
	};
	Parse = $pkg.Parse = function(layout, value) {
		var layout, value;
		return parse(layout, value, $pkg.UTC, $pkg.Local);
	};
	parse = function(layout, value, defaultLocation, local) {
		var _ref, _ref$1, _ref$2, _ref$3, _tmp, _tmp$1, _tmp$10, _tmp$11, _tmp$12, _tmp$13, _tmp$14, _tmp$15, _tmp$16, _tmp$17, _tmp$18, _tmp$19, _tmp$2, _tmp$20, _tmp$21, _tmp$22, _tmp$23, _tmp$24, _tmp$25, _tmp$26, _tmp$27, _tmp$28, _tmp$29, _tmp$3, _tmp$30, _tmp$31, _tmp$32, _tmp$33, _tmp$34, _tmp$35, _tmp$36, _tmp$37, _tmp$38, _tmp$39, _tmp$4, _tmp$40, _tmp$41, _tmp$42, _tmp$43, _tmp$5, _tmp$6, _tmp$7, _tmp$8, _tmp$9, _tuple$1, _tuple$10, _tuple$11, _tuple$12, _tuple$13, _tuple$14, _tuple$15, _tuple$16, _tuple$17, _tuple$18, _tuple$19, _tuple$2, _tuple$20, _tuple$21, _tuple$22, _tuple$23, _tuple$24, _tuple$25, _tuple$3, _tuple$4, _tuple$5, _tuple$6, _tuple$7, _tuple$8, _tuple$9, alayout, amSet, avalue, day, defaultLocation, err, hour, hour$1, hr, i, layout, local, min, min$1, mm, month, n, n$1, name, ndigit, nsec, offset, offset$1, ok, ok$1, p, pmSet, prefix, rangeErrString, sec, seconds, sign, ss, std, stdstr, suffix, t, t$1, value, x, x$1, x$2, x$3, x$4, x$5, year, z, zoneName, zoneOffset;
		_tmp = layout; _tmp$1 = value; alayout = _tmp; avalue = _tmp$1;
		rangeErrString = "";
		amSet = false;
		pmSet = false;
		year = 0;
		month = 1;
		day = 1;
		hour = 0;
		min = 0;
		sec = 0;
		nsec = 0;
		z = ptrType$1.nil;
		zoneOffset = -1;
		zoneName = "";
		while (true) {
			err = $ifaceNil;
			_tuple$1 = nextStdChunk(layout); prefix = _tuple$1[0]; std = _tuple$1[1]; suffix = _tuple$1[2];
			stdstr = layout.substring(prefix.length, (layout.length - suffix.length >> 0));
			_tuple$2 = skip(value, prefix); value = _tuple$2[0]; err = _tuple$2[1];
			if (!($interfaceIsEqual(err, $ifaceNil))) {
				return [new Time.ptr(new $Int64(0, 0), 0, ptrType$2.nil), new ParseError.ptr(alayout, avalue, prefix, value, "")];
			}
			if (std === 0) {
				if (!((value.length === 0))) {
					return [new Time.ptr(new $Int64(0, 0), 0, ptrType$2.nil), new ParseError.ptr(alayout, avalue, "", value, ": extra text: " + value)];
				}
				break;
			}
			layout = suffix;
			p = "";
			_ref = std & 65535;
			switch (0) { default: if (_ref === 274) {
				if (value.length < 2) {
					err = errBad;
					break;
				}
				_tmp$2 = value.substring(0, 2); _tmp$3 = value.substring(2); p = _tmp$2; value = _tmp$3;
				_tuple$3 = atoi(p); year = _tuple$3[0]; err = _tuple$3[1];
				if (year >= 69) {
					year = year + (1900) >> 0;
				} else {
					year = year + (2000) >> 0;
				}
			} else if (_ref === 273) {
				if (value.length < 4 || !isDigit(value, 0)) {
					err = errBad;
					break;
				}
				_tmp$4 = value.substring(0, 4); _tmp$5 = value.substring(4); p = _tmp$4; value = _tmp$5;
				_tuple$4 = atoi(p); year = _tuple$4[0]; err = _tuple$4[1];
			} else if (_ref === 258) {
				_tuple$5 = lookup(shortMonthNames, value); month = _tuple$5[0]; value = _tuple$5[1]; err = _tuple$5[2];
			} else if (_ref === 257) {
				_tuple$6 = lookup(longMonthNames, value); month = _tuple$6[0]; value = _tuple$6[1]; err = _tuple$6[2];
			} else if (_ref === 259 || _ref === 260) {
				_tuple$7 = getnum(value, std === 260); month = _tuple$7[0]; value = _tuple$7[1]; err = _tuple$7[2];
				if (month <= 0 || 12 < month) {
					rangeErrString = "month";
				}
			} else if (_ref === 262) {
				_tuple$8 = lookup(shortDayNames, value); value = _tuple$8[1]; err = _tuple$8[2];
			} else if (_ref === 261) {
				_tuple$9 = lookup(longDayNames, value); value = _tuple$9[1]; err = _tuple$9[2];
			} else if (_ref === 263 || _ref === 264 || _ref === 265) {
				if ((std === 264) && value.length > 0 && (value.charCodeAt(0) === 32)) {
					value = value.substring(1);
				}
				_tuple$10 = getnum(value, std === 265); day = _tuple$10[0]; value = _tuple$10[1]; err = _tuple$10[2];
				if (day < 0 || 31 < day) {
					rangeErrString = "day";
				}
			} else if (_ref === 522) {
				_tuple$11 = getnum(value, false); hour = _tuple$11[0]; value = _tuple$11[1]; err = _tuple$11[2];
				if (hour < 0 || 24 <= hour) {
					rangeErrString = "hour";
				}
			} else if (_ref === 523 || _ref === 524) {
				_tuple$12 = getnum(value, std === 524); hour = _tuple$12[0]; value = _tuple$12[1]; err = _tuple$12[2];
				if (hour < 0 || 12 < hour) {
					rangeErrString = "hour";
				}
			} else if (_ref === 525 || _ref === 526) {
				_tuple$13 = getnum(value, std === 526); min = _tuple$13[0]; value = _tuple$13[1]; err = _tuple$13[2];
				if (min < 0 || 60 <= min) {
					rangeErrString = "minute";
				}
			} else if (_ref === 527 || _ref === 528) {
				_tuple$14 = getnum(value, std === 528); sec = _tuple$14[0]; value = _tuple$14[1]; err = _tuple$14[2];
				if (sec < 0 || 60 <= sec) {
					rangeErrString = "second";
				}
				if (value.length >= 2 && (value.charCodeAt(0) === 46) && isDigit(value, 1)) {
					_tuple$15 = nextStdChunk(layout); std = _tuple$15[1];
					std = std & (65535);
					if ((std === 31) || (std === 32)) {
						break;
					}
					n = 2;
					while (true) {
						if (!(n < value.length && isDigit(value, n))) { break; }
						n = n + (1) >> 0;
					}
					_tuple$16 = parseNanoseconds(value, n); nsec = _tuple$16[0]; rangeErrString = _tuple$16[1]; err = _tuple$16[2];
					value = value.substring(n);
				}
			} else if (_ref === 531) {
				if (value.length < 2) {
					err = errBad;
					break;
				}
				_tmp$6 = value.substring(0, 2); _tmp$7 = value.substring(2); p = _tmp$6; value = _tmp$7;
				_ref$1 = p;
				if (_ref$1 === "PM") {
					pmSet = true;
				} else if (_ref$1 === "AM") {
					amSet = true;
				} else {
					err = errBad;
				}
			} else if (_ref === 532) {
				if (value.length < 2) {
					err = errBad;
					break;
				}
				_tmp$8 = value.substring(0, 2); _tmp$9 = value.substring(2); p = _tmp$8; value = _tmp$9;
				_ref$2 = p;
				if (_ref$2 === "pm") {
					pmSet = true;
				} else if (_ref$2 === "am") {
					amSet = true;
				} else {
					err = errBad;
				}
			} else if (_ref === 22 || _ref === 24 || _ref === 23 || _ref === 25 || _ref === 26 || _ref === 28 || _ref === 29 || _ref === 27 || _ref === 30) {
				if (((std === 22) || (std === 24)) && value.length >= 1 && (value.charCodeAt(0) === 90)) {
					value = value.substring(1);
					z = $pkg.UTC;
					break;
				}
				_tmp$10 = ""; _tmp$11 = ""; _tmp$12 = ""; _tmp$13 = ""; sign = _tmp$10; hour$1 = _tmp$11; min$1 = _tmp$12; seconds = _tmp$13;
				if ((std === 24) || (std === 29)) {
					if (value.length < 6) {
						err = errBad;
						break;
					}
					if (!((value.charCodeAt(3) === 58))) {
						err = errBad;
						break;
					}
					_tmp$14 = value.substring(0, 1); _tmp$15 = value.substring(1, 3); _tmp$16 = value.substring(4, 6); _tmp$17 = "00"; _tmp$18 = value.substring(6); sign = _tmp$14; hour$1 = _tmp$15; min$1 = _tmp$16; seconds = _tmp$17; value = _tmp$18;
				} else if (std === 28) {
					if (value.length < 3) {
						err = errBad;
						break;
					}
					_tmp$19 = value.substring(0, 1); _tmp$20 = value.substring(1, 3); _tmp$21 = "00"; _tmp$22 = "00"; _tmp$23 = value.substring(3); sign = _tmp$19; hour$1 = _tmp$20; min$1 = _tmp$21; seconds = _tmp$22; value = _tmp$23;
				} else if ((std === 25) || (std === 30)) {
					if (value.length < 9) {
						err = errBad;
						break;
					}
					if (!((value.charCodeAt(3) === 58)) || !((value.charCodeAt(6) === 58))) {
						err = errBad;
						break;
					}
					_tmp$24 = value.substring(0, 1); _tmp$25 = value.substring(1, 3); _tmp$26 = value.substring(4, 6); _tmp$27 = value.substring(7, 9); _tmp$28 = value.substring(9); sign = _tmp$24; hour$1 = _tmp$25; min$1 = _tmp$26; seconds = _tmp$27; value = _tmp$28;
				} else if ((std === 23) || (std === 27)) {
					if (value.length < 7) {
						err = errBad;
						break;
					}
					_tmp$29 = value.substring(0, 1); _tmp$30 = value.substring(1, 3); _tmp$31 = value.substring(3, 5); _tmp$32 = value.substring(5, 7); _tmp$33 = value.substring(7); sign = _tmp$29; hour$1 = _tmp$30; min$1 = _tmp$31; seconds = _tmp$32; value = _tmp$33;
				} else {
					if (value.length < 5) {
						err = errBad;
						break;
					}
					_tmp$34 = value.substring(0, 1); _tmp$35 = value.substring(1, 3); _tmp$36 = value.substring(3, 5); _tmp$37 = "00"; _tmp$38 = value.substring(5); sign = _tmp$34; hour$1 = _tmp$35; min$1 = _tmp$36; seconds = _tmp$37; value = _tmp$38;
				}
				_tmp$39 = 0; _tmp$40 = 0; _tmp$41 = 0; hr = _tmp$39; mm = _tmp$40; ss = _tmp$41;
				_tuple$17 = atoi(hour$1); hr = _tuple$17[0]; err = _tuple$17[1];
				if ($interfaceIsEqual(err, $ifaceNil)) {
					_tuple$18 = atoi(min$1); mm = _tuple$18[0]; err = _tuple$18[1];
				}
				if ($interfaceIsEqual(err, $ifaceNil)) {
					_tuple$19 = atoi(seconds); ss = _tuple$19[0]; err = _tuple$19[1];
				}
				zoneOffset = ((((hr * 60 >> 0) + mm >> 0)) * 60 >> 0) + ss >> 0;
				_ref$3 = sign.charCodeAt(0);
				if (_ref$3 === 43) {
				} else if (_ref$3 === 45) {
					zoneOffset = -zoneOffset;
				} else {
					err = errBad;
				}
			} else if (_ref === 21) {
				if (value.length >= 3 && value.substring(0, 3) === "UTC") {
					z = $pkg.UTC;
					value = value.substring(3);
					break;
				}
				_tuple$20 = parseTimeZone(value); n$1 = _tuple$20[0]; ok = _tuple$20[1];
				if (!ok) {
					err = errBad;
					break;
				}
				_tmp$42 = value.substring(0, n$1); _tmp$43 = value.substring(n$1); zoneName = _tmp$42; value = _tmp$43;
			} else if (_ref === 31) {
				ndigit = 1 + ((std >> 16 >> 0)) >> 0;
				if (value.length < ndigit) {
					err = errBad;
					break;
				}
				_tuple$21 = parseNanoseconds(value, ndigit); nsec = _tuple$21[0]; rangeErrString = _tuple$21[1]; err = _tuple$21[2];
				value = value.substring(ndigit);
			} else if (_ref === 32) {
				if (value.length < 2 || !((value.charCodeAt(0) === 46)) || value.charCodeAt(1) < 48 || 57 < value.charCodeAt(1)) {
					break;
				}
				i = 0;
				while (true) {
					if (!(i < 9 && (i + 1 >> 0) < value.length && 48 <= value.charCodeAt((i + 1 >> 0)) && value.charCodeAt((i + 1 >> 0)) <= 57)) { break; }
					i = i + (1) >> 0;
				}
				_tuple$22 = parseNanoseconds(value, 1 + i >> 0); nsec = _tuple$22[0]; rangeErrString = _tuple$22[1]; err = _tuple$22[2];
				value = value.substring((1 + i >> 0));
			} }
			if (!(rangeErrString === "")) {
				return [new Time.ptr(new $Int64(0, 0), 0, ptrType$2.nil), new ParseError.ptr(alayout, avalue, stdstr, value, ": " + rangeErrString + " out of range")];
			}
			if (!($interfaceIsEqual(err, $ifaceNil))) {
				return [new Time.ptr(new $Int64(0, 0), 0, ptrType$2.nil), new ParseError.ptr(alayout, avalue, stdstr, value, "")];
			}
		}
		if (pmSet && hour < 12) {
			hour = hour + (12) >> 0;
		} else if (amSet && (hour === 12)) {
			hour = 0;
		}
		if (!(z === ptrType$1.nil)) {
			return [Date(year, (month >> 0), day, hour, min, sec, nsec, z), $ifaceNil];
		}
		if (!((zoneOffset === -1))) {
			t = $clone(Date(year, (month >> 0), day, hour, min, sec, nsec, $pkg.UTC), Time);
			t.sec = (x = t.sec, x$1 = new $Int64(0, zoneOffset), new $Int64(x.$high - x$1.$high, x.$low - x$1.$low));
			_tuple$23 = local.lookup((x$2 = t.sec, new $Int64(x$2.$high + -15, x$2.$low + 2288912640))); name = _tuple$23[0]; offset = _tuple$23[1];
			if ((offset === zoneOffset) && (zoneName === "" || name === zoneName)) {
				t.loc = local;
				return [t, $ifaceNil];
			}
			t.loc = FixedZone(zoneName, zoneOffset);
			return [t, $ifaceNil];
		}
		if (!(zoneName === "")) {
			t$1 = $clone(Date(year, (month >> 0), day, hour, min, sec, nsec, $pkg.UTC), Time);
			_tuple$24 = local.lookupName(zoneName, (x$3 = t$1.sec, new $Int64(x$3.$high + -15, x$3.$low + 2288912640))); offset$1 = _tuple$24[0]; ok$1 = _tuple$24[2];
			if (ok$1) {
				t$1.sec = (x$4 = t$1.sec, x$5 = new $Int64(0, offset$1), new $Int64(x$4.$high - x$5.$high, x$4.$low - x$5.$low));
				t$1.loc = local;
				return [t$1, $ifaceNil];
			}
			if (zoneName.length > 3 && zoneName.substring(0, 3) === "GMT") {
				_tuple$25 = atoi(zoneName.substring(3)); offset$1 = _tuple$25[0];
				offset$1 = offset$1 * (3600) >> 0;
			}
			t$1.loc = FixedZone(zoneName, offset$1);
			return [t$1, $ifaceNil];
		}
		return [Date(year, (month >> 0), day, hour, min, sec, nsec, defaultLocation), $ifaceNil];
	};
	parseTimeZone = function(value) {
		var _ref, _tmp, _tmp$1, _tmp$10, _tmp$11, _tmp$12, _tmp$13, _tmp$14, _tmp$15, _tmp$2, _tmp$3, _tmp$4, _tmp$5, _tmp$6, _tmp$7, _tmp$8, _tmp$9, c, length = 0, nUpper, ok = false, value;
		if (value.length < 3) {
			_tmp = 0; _tmp$1 = false; length = _tmp; ok = _tmp$1;
			return [length, ok];
		}
		if (value.length >= 4 && (value.substring(0, 4) === "ChST" || value.substring(0, 4) === "MeST")) {
			_tmp$2 = 4; _tmp$3 = true; length = _tmp$2; ok = _tmp$3;
			return [length, ok];
		}
		if (value.substring(0, 3) === "GMT") {
			length = parseGMT(value);
			_tmp$4 = length; _tmp$5 = true; length = _tmp$4; ok = _tmp$5;
			return [length, ok];
		}
		nUpper = 0;
		nUpper = 0;
		while (true) {
			if (!(nUpper < 6)) { break; }
			if (nUpper >= value.length) {
				break;
			}
			c = value.charCodeAt(nUpper);
			if (c < 65 || 90 < c) {
				break;
			}
			nUpper = nUpper + (1) >> 0;
		}
		_ref = nUpper;
		if (_ref === 0 || _ref === 1 || _ref === 2 || _ref === 6) {
			_tmp$6 = 0; _tmp$7 = false; length = _tmp$6; ok = _tmp$7;
			return [length, ok];
		} else if (_ref === 5) {
			if (value.charCodeAt(4) === 84) {
				_tmp$8 = 5; _tmp$9 = true; length = _tmp$8; ok = _tmp$9;
				return [length, ok];
			}
		} else if (_ref === 4) {
			if (value.charCodeAt(3) === 84) {
				_tmp$10 = 4; _tmp$11 = true; length = _tmp$10; ok = _tmp$11;
				return [length, ok];
			}
		} else if (_ref === 3) {
			_tmp$12 = 3; _tmp$13 = true; length = _tmp$12; ok = _tmp$13;
			return [length, ok];
		}
		_tmp$14 = 0; _tmp$15 = false; length = _tmp$14; ok = _tmp$15;
		return [length, ok];
	};
	parseGMT = function(value) {
		var _tuple$1, err, rem, sign, value, x;
		value = value.substring(3);
		if (value.length === 0) {
			return 3;
		}
		sign = value.charCodeAt(0);
		if (!((sign === 45)) && !((sign === 43))) {
			return 3;
		}
		_tuple$1 = leadingInt(value.substring(1)); x = _tuple$1[0]; rem = _tuple$1[1]; err = _tuple$1[2];
		if (!($interfaceIsEqual(err, $ifaceNil))) {
			return 3;
		}
		if (sign === 45) {
			x = new $Int64(-x.$high, -x.$low);
		}
		if ((x.$high === 0 && x.$low === 0) || (x.$high < -1 || (x.$high === -1 && x.$low < 4294967282)) || (0 < x.$high || (0 === x.$high && 12 < x.$low))) {
			return 3;
		}
		return (3 + value.length >> 0) - rem.length >> 0;
	};
	parseNanoseconds = function(value, nbytes) {
		var _tuple$1, err = $ifaceNil, i, nbytes, ns = 0, rangeErrString = "", scaleDigits, value;
		if (!((value.charCodeAt(0) === 46))) {
			err = errBad;
			return [ns, rangeErrString, err];
		}
		_tuple$1 = atoi(value.substring(1, nbytes)); ns = _tuple$1[0]; err = _tuple$1[1];
		if (!($interfaceIsEqual(err, $ifaceNil))) {
			return [ns, rangeErrString, err];
		}
		if (ns < 0 || 1000000000 <= ns) {
			rangeErrString = "fractional second";
			return [ns, rangeErrString, err];
		}
		scaleDigits = 10 - nbytes >> 0;
		i = 0;
		while (true) {
			if (!(i < scaleDigits)) { break; }
			ns = ns * (10) >> 0;
			i = i + (1) >> 0;
		}
		return [ns, rangeErrString, err];
	};
	leadingInt = function(s) {
		var _tmp, _tmp$1, _tmp$2, _tmp$3, _tmp$4, _tmp$5, c, err = $ifaceNil, i, rem = "", s, x = new $Int64(0, 0), x$1, x$2, x$3;
		i = 0;
		while (true) {
			if (!(i < s.length)) { break; }
			c = s.charCodeAt(i);
			if (c < 48 || c > 57) {
				break;
			}
			if ((x.$high > 214748364 || (x.$high === 214748364 && x.$low >= 3435973835))) {
				_tmp = new $Int64(0, 0); _tmp$1 = ""; _tmp$2 = errLeadingInt; x = _tmp; rem = _tmp$1; err = _tmp$2;
				return [x, rem, err];
			}
			x = (x$1 = (x$2 = $mul64(x, new $Int64(0, 10)), x$3 = new $Int64(0, c), new $Int64(x$2.$high + x$3.$high, x$2.$low + x$3.$low)), new $Int64(x$1.$high - 0, x$1.$low - 48));
			i = i + (1) >> 0;
		}
		_tmp$3 = x; _tmp$4 = s.substring(i); _tmp$5 = $ifaceNil; x = _tmp$3; rem = _tmp$4; err = _tmp$5;
		return [x, rem, err];
	};
	Time.ptr.prototype.After = function(u) {
		var t, u, x, x$1, x$2, x$3;
		t = $clone(this, Time);
		u = $clone(u, Time);
		return (x = t.sec, x$1 = u.sec, (x.$high > x$1.$high || (x.$high === x$1.$high && x.$low > x$1.$low))) || (x$2 = t.sec, x$3 = u.sec, (x$2.$high === x$3.$high && x$2.$low === x$3.$low)) && t.nsec > u.nsec;
	};
	Time.prototype.After = function(u) { return this.$val.After(u); };
	Time.ptr.prototype.Before = function(u) {
		var t, u, x, x$1, x$2, x$3;
		t = $clone(this, Time);
		u = $clone(u, Time);
		return (x = t.sec, x$1 = u.sec, (x.$high < x$1.$high || (x.$high === x$1.$high && x.$low < x$1.$low))) || (x$2 = t.sec, x$3 = u.sec, (x$2.$high === x$3.$high && x$2.$low === x$3.$low)) && t.nsec < u.nsec;
	};
	Time.prototype.Before = function(u) { return this.$val.Before(u); };
	Time.ptr.prototype.Equal = function(u) {
		var t, u, x, x$1;
		t = $clone(this, Time);
		u = $clone(u, Time);
		return (x = t.sec, x$1 = u.sec, (x.$high === x$1.$high && x.$low === x$1.$low)) && (t.nsec === u.nsec);
	};
	Time.prototype.Equal = function(u) { return this.$val.Equal(u); };
	Month.prototype.String = function() {
		var m, x;
		m = this.$val;
		return (x = m - 1 >> 0, ((x < 0 || x >= months.length) ? $throwRuntimeError("index out of range") : months[x]));
	};
	$ptrType(Month).prototype.String = function() { return new Month(this.$get()).String(); };
	Weekday.prototype.String = function() {
		var d;
		d = this.$val;
		return ((d < 0 || d >= days.length) ? $throwRuntimeError("index out of range") : days[d]);
	};
	$ptrType(Weekday).prototype.String = function() { return new Weekday(this.$get()).String(); };
	Time.ptr.prototype.IsZero = function() {
		var t, x;
		t = $clone(this, Time);
		return (x = t.sec, (x.$high === 0 && x.$low === 0)) && (t.nsec === 0);
	};
	Time.prototype.IsZero = function() { return this.$val.IsZero(); };
	Time.ptr.prototype.abs = function() {
		var _tuple$1, l, offset, sec, t, x, x$1, x$2, x$3, x$4, x$5;
		t = $clone(this, Time);
		l = t.loc;
		if (l === ptrType$2.nil || l === localLoc) {
			l = l.get();
		}
		sec = (x = t.sec, new $Int64(x.$high + -15, x.$low + 2288912640));
		if (!(l === utcLoc)) {
			if (!(l.cacheZone === ptrType.nil) && (x$1 = l.cacheStart, (x$1.$high < sec.$high || (x$1.$high === sec.$high && x$1.$low <= sec.$low))) && (x$2 = l.cacheEnd, (sec.$high < x$2.$high || (sec.$high === x$2.$high && sec.$low < x$2.$low)))) {
				sec = (x$3 = new $Int64(0, l.cacheZone.offset), new $Int64(sec.$high + x$3.$high, sec.$low + x$3.$low));
			} else {
				_tuple$1 = l.lookup(sec); offset = _tuple$1[1];
				sec = (x$4 = new $Int64(0, offset), new $Int64(sec.$high + x$4.$high, sec.$low + x$4.$low));
			}
		}
		return (x$5 = new $Int64(sec.$high + 2147483646, sec.$low + 450480384), new $Uint64(x$5.$high, x$5.$low));
	};
	Time.prototype.abs = function() { return this.$val.abs(); };
	Time.ptr.prototype.locabs = function() {
		var _tuple$1, abs = new $Uint64(0, 0), l, name = "", offset = 0, sec, t, x, x$1, x$2, x$3, x$4;
		t = $clone(this, Time);
		l = t.loc;
		if (l === ptrType$2.nil || l === localLoc) {
			l = l.get();
		}
		sec = (x = t.sec, new $Int64(x.$high + -15, x.$low + 2288912640));
		if (!(l === utcLoc)) {
			if (!(l.cacheZone === ptrType.nil) && (x$1 = l.cacheStart, (x$1.$high < sec.$high || (x$1.$high === sec.$high && x$1.$low <= sec.$low))) && (x$2 = l.cacheEnd, (sec.$high < x$2.$high || (sec.$high === x$2.$high && sec.$low < x$2.$low)))) {
				name = l.cacheZone.name;
				offset = l.cacheZone.offset;
			} else {
				_tuple$1 = l.lookup(sec); name = _tuple$1[0]; offset = _tuple$1[1];
			}
			sec = (x$3 = new $Int64(0, offset), new $Int64(sec.$high + x$3.$high, sec.$low + x$3.$low));
		} else {
			name = "UTC";
		}
		abs = (x$4 = new $Int64(sec.$high + 2147483646, sec.$low + 450480384), new $Uint64(x$4.$high, x$4.$low));
		return [name, offset, abs];
	};
	Time.prototype.locabs = function() { return this.$val.locabs(); };
	Time.ptr.prototype.Date = function() {
		var _tuple$1, day = 0, month = 0, t, year = 0;
		t = $clone(this, Time);
		_tuple$1 = t.date(true); year = _tuple$1[0]; month = _tuple$1[1]; day = _tuple$1[2];
		return [year, month, day];
	};
	Time.prototype.Date = function() { return this.$val.Date(); };
	Time.ptr.prototype.Year = function() {
		var _tuple$1, t, year;
		t = $clone(this, Time);
		_tuple$1 = t.date(false); year = _tuple$1[0];
		return year;
	};
	Time.prototype.Year = function() { return this.$val.Year(); };
	Time.ptr.prototype.Month = function() {
		var _tuple$1, month, t;
		t = $clone(this, Time);
		_tuple$1 = t.date(true); month = _tuple$1[1];
		return month;
	};
	Time.prototype.Month = function() { return this.$val.Month(); };
	Time.ptr.prototype.Day = function() {
		var _tuple$1, day, t;
		t = $clone(this, Time);
		_tuple$1 = t.date(true); day = _tuple$1[2];
		return day;
	};
	Time.prototype.Day = function() { return this.$val.Day(); };
	Time.ptr.prototype.Weekday = function() {
		var t;
		t = $clone(this, Time);
		return absWeekday(t.abs());
	};
	Time.prototype.Weekday = function() { return this.$val.Weekday(); };
	absWeekday = function(abs) {
		var _q, abs, sec;
		sec = $div64((new $Uint64(abs.$high + 0, abs.$low + 86400)), new $Uint64(0, 604800), true);
		return ((_q = (sec.$low >> 0) / 86400, (_q === _q && _q !== 1/0 && _q !== -1/0) ? _q >> 0 : $throwRuntimeError("integer divide by zero")) >> 0);
	};
	Time.ptr.prototype.ISOWeek = function() {
		var _q, _r$1, _r$2, _r$3, _tuple$1, day, dec31wday, jan1wday, month, t, wday, week = 0, yday, year = 0;
		t = $clone(this, Time);
		_tuple$1 = t.date(true); year = _tuple$1[0]; month = _tuple$1[1]; day = _tuple$1[2]; yday = _tuple$1[3];
		wday = (_r$1 = ((t.Weekday() + 6 >> 0) >> 0) % 7, _r$1 === _r$1 ? _r$1 : $throwRuntimeError("integer divide by zero"));
		week = (_q = (((yday - wday >> 0) + 7 >> 0)) / 7, (_q === _q && _q !== 1/0 && _q !== -1/0) ? _q >> 0 : $throwRuntimeError("integer divide by zero"));
		jan1wday = (_r$2 = (((wday - yday >> 0) + 371 >> 0)) % 7, _r$2 === _r$2 ? _r$2 : $throwRuntimeError("integer divide by zero"));
		if (1 <= jan1wday && jan1wday <= 3) {
			week = week + (1) >> 0;
		}
		if (week === 0) {
			year = year - (1) >> 0;
			week = 52;
			if ((jan1wday === 4) || ((jan1wday === 5) && isLeap(year))) {
				week = week + (1) >> 0;
			}
		}
		if ((month === 12) && day >= 29 && wday < 3) {
			dec31wday = (_r$3 = (((wday + 31 >> 0) - day >> 0)) % 7, _r$3 === _r$3 ? _r$3 : $throwRuntimeError("integer divide by zero"));
			if (0 <= dec31wday && dec31wday <= 2) {
				year = year + (1) >> 0;
				week = 1;
			}
		}
		return [year, week];
	};
	Time.prototype.ISOWeek = function() { return this.$val.ISOWeek(); };
	Time.ptr.prototype.Clock = function() {
		var _tuple$1, hour = 0, min = 0, sec = 0, t;
		t = $clone(this, Time);
		_tuple$1 = absClock(t.abs()); hour = _tuple$1[0]; min = _tuple$1[1]; sec = _tuple$1[2];
		return [hour, min, sec];
	};
	Time.prototype.Clock = function() { return this.$val.Clock(); };
	absClock = function(abs) {
		var _q, _q$1, abs, hour = 0, min = 0, sec = 0;
		sec = ($div64(abs, new $Uint64(0, 86400), true).$low >> 0);
		hour = (_q = sec / 3600, (_q === _q && _q !== 1/0 && _q !== -1/0) ? _q >> 0 : $throwRuntimeError("integer divide by zero"));
		sec = sec - ((hour * 3600 >> 0)) >> 0;
		min = (_q$1 = sec / 60, (_q$1 === _q$1 && _q$1 !== 1/0 && _q$1 !== -1/0) ? _q$1 >> 0 : $throwRuntimeError("integer divide by zero"));
		sec = sec - ((min * 60 >> 0)) >> 0;
		return [hour, min, sec];
	};
	Time.ptr.prototype.Hour = function() {
		var _q, t;
		t = $clone(this, Time);
		return (_q = ($div64(t.abs(), new $Uint64(0, 86400), true).$low >> 0) / 3600, (_q === _q && _q !== 1/0 && _q !== -1/0) ? _q >> 0 : $throwRuntimeError("integer divide by zero"));
	};
	Time.prototype.Hour = function() { return this.$val.Hour(); };
	Time.ptr.prototype.Minute = function() {
		var _q, t;
		t = $clone(this, Time);
		return (_q = ($div64(t.abs(), new $Uint64(0, 3600), true).$low >> 0) / 60, (_q === _q && _q !== 1/0 && _q !== -1/0) ? _q >> 0 : $throwRuntimeError("integer divide by zero"));
	};
	Time.prototype.Minute = function() { return this.$val.Minute(); };
	Time.ptr.prototype.Second = function() {
		var t;
		t = $clone(this, Time);
		return ($div64(t.abs(), new $Uint64(0, 60), true).$low >> 0);
	};
	Time.prototype.Second = function() { return this.$val.Second(); };
	Time.ptr.prototype.Nanosecond = function() {
		var t;
		t = $clone(this, Time);
		return (t.nsec >> 0);
	};
	Time.prototype.Nanosecond = function() { return this.$val.Nanosecond(); };
	Time.ptr.prototype.YearDay = function() {
		var _tuple$1, t, yday;
		t = $clone(this, Time);
		_tuple$1 = t.date(false); yday = _tuple$1[3];
		return yday + 1 >> 0;
	};
	Time.prototype.YearDay = function() { return this.$val.YearDay(); };
	Duration.prototype.String = function() {
		var _tuple$1, _tuple$2, buf, d, neg, prec, u, w;
		d = this;
		buf = $clone(arrayType$4.zero(), arrayType$4);
		w = 32;
		u = new $Uint64(d.$high, d.$low);
		neg = (d.$high < 0 || (d.$high === 0 && d.$low < 0));
		if (neg) {
			u = new $Uint64(-u.$high, -u.$low);
		}
		if ((u.$high < 0 || (u.$high === 0 && u.$low < 1000000000))) {
			prec = 0;
			w = w - (1) >> 0;
			((w < 0 || w >= buf.length) ? $throwRuntimeError("index out of range") : buf[w] = 115);
			w = w - (1) >> 0;
			if ((u.$high === 0 && u.$low === 0)) {
				return "0";
			} else if ((u.$high < 0 || (u.$high === 0 && u.$low < 1000))) {
				prec = 0;
				((w < 0 || w >= buf.length) ? $throwRuntimeError("index out of range") : buf[w] = 110);
			} else if ((u.$high < 0 || (u.$high === 0 && u.$low < 1000000))) {
				prec = 3;
				w = w - (1) >> 0;
				$copyString($subslice(new sliceType$17(buf), w), "\xC2\xB5");
			} else {
				prec = 6;
				((w < 0 || w >= buf.length) ? $throwRuntimeError("index out of range") : buf[w] = 109);
			}
			_tuple$1 = fmtFrac($subslice(new sliceType$18(buf), 0, w), u, prec); w = _tuple$1[0]; u = _tuple$1[1];
			w = fmtInt($subslice(new sliceType$19(buf), 0, w), u);
		} else {
			w = w - (1) >> 0;
			((w < 0 || w >= buf.length) ? $throwRuntimeError("index out of range") : buf[w] = 115);
			_tuple$2 = fmtFrac($subslice(new sliceType$20(buf), 0, w), u, 9); w = _tuple$2[0]; u = _tuple$2[1];
			w = fmtInt($subslice(new sliceType$21(buf), 0, w), $div64(u, new $Uint64(0, 60), true));
			u = $div64(u, (new $Uint64(0, 60)), false);
			if ((u.$high > 0 || (u.$high === 0 && u.$low > 0))) {
				w = w - (1) >> 0;
				((w < 0 || w >= buf.length) ? $throwRuntimeError("index out of range") : buf[w] = 109);
				w = fmtInt($subslice(new sliceType$22(buf), 0, w), $div64(u, new $Uint64(0, 60), true));
				u = $div64(u, (new $Uint64(0, 60)), false);
				if ((u.$high > 0 || (u.$high === 0 && u.$low > 0))) {
					w = w - (1) >> 0;
					((w < 0 || w >= buf.length) ? $throwRuntimeError("index out of range") : buf[w] = 104);
					w = fmtInt($subslice(new sliceType$23(buf), 0, w), u);
				}
			}
		}
		if (neg) {
			w = w - (1) >> 0;
			((w < 0 || w >= buf.length) ? $throwRuntimeError("index out of range") : buf[w] = 45);
		}
		return $bytesToString($subslice(new sliceType$24(buf), w));
	};
	$ptrType(Duration).prototype.String = function() { return this.$get().String(); };
	fmtFrac = function(buf, v, prec) {
		var _tmp, _tmp$1, buf, digit, i, nv = new $Uint64(0, 0), nw = 0, prec, print, v, w;
		w = buf.$length;
		print = false;
		i = 0;
		while (true) {
			if (!(i < prec)) { break; }
			digit = $div64(v, new $Uint64(0, 10), true);
			print = print || !((digit.$high === 0 && digit.$low === 0));
			if (print) {
				w = w - (1) >> 0;
				((w < 0 || w >= buf.$length) ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + w] = (digit.$low << 24 >>> 24) + 48 << 24 >>> 24);
			}
			v = $div64(v, (new $Uint64(0, 10)), false);
			i = i + (1) >> 0;
		}
		if (print) {
			w = w - (1) >> 0;
			((w < 0 || w >= buf.$length) ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + w] = 46);
		}
		_tmp = w; _tmp$1 = v; nw = _tmp; nv = _tmp$1;
		return [nw, nv];
	};
	fmtInt = function(buf, v) {
		var buf, v, w;
		w = buf.$length;
		if ((v.$high === 0 && v.$low === 0)) {
			w = w - (1) >> 0;
			((w < 0 || w >= buf.$length) ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + w] = 48);
		} else {
			while (true) {
				if (!((v.$high > 0 || (v.$high === 0 && v.$low > 0)))) { break; }
				w = w - (1) >> 0;
				((w < 0 || w >= buf.$length) ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + w] = ($div64(v, new $Uint64(0, 10), true).$low << 24 >>> 24) + 48 << 24 >>> 24);
				v = $div64(v, (new $Uint64(0, 10)), false);
			}
		}
		return w;
	};
	Duration.prototype.Nanoseconds = function() {
		var d;
		d = this;
		return new $Int64(d.$high, d.$low);
	};
	$ptrType(Duration).prototype.Nanoseconds = function() { return this.$get().Nanoseconds(); };
	Duration.prototype.Seconds = function() {
		var d, nsec, sec;
		d = this;
		sec = $div64(d, new Duration(0, 1000000000), false);
		nsec = $div64(d, new Duration(0, 1000000000), true);
		return $flatten64(sec) + $flatten64(nsec) * 1e-09;
	};
	$ptrType(Duration).prototype.Seconds = function() { return this.$get().Seconds(); };
	Duration.prototype.Minutes = function() {
		var d, min, nsec;
		d = this;
		min = $div64(d, new Duration(13, 4165425152), false);
		nsec = $div64(d, new Duration(13, 4165425152), true);
		return $flatten64(min) + $flatten64(nsec) * 1.6666666666666667e-11;
	};
	$ptrType(Duration).prototype.Minutes = function() { return this.$get().Minutes(); };
	Duration.prototype.Hours = function() {
		var d, hour, nsec;
		d = this;
		hour = $div64(d, new Duration(838, 817405952), false);
		nsec = $div64(d, new Duration(838, 817405952), true);
		return $flatten64(hour) + $flatten64(nsec) * 2.777777777777778e-13;
	};
	$ptrType(Duration).prototype.Hours = function() { return this.$get().Hours(); };
	Time.ptr.prototype.Add = function(d) {
		var d, nsec, t, x, x$1, x$2, x$3, x$4, x$5, x$6, x$7;
		t = $clone(this, Time);
		t.sec = (x = t.sec, x$1 = (x$2 = $div64(d, new Duration(0, 1000000000), false), new $Int64(x$2.$high, x$2.$low)), new $Int64(x.$high + x$1.$high, x.$low + x$1.$low));
		nsec = t.nsec + ((x$3 = $div64(d, new Duration(0, 1000000000), true), x$3.$low + ((x$3.$high >> 31) * 4294967296)) >> 0) >> 0;
		if (nsec >= 1000000000) {
			t.sec = (x$4 = t.sec, x$5 = new $Int64(0, 1), new $Int64(x$4.$high + x$5.$high, x$4.$low + x$5.$low));
			nsec = nsec - (1000000000) >> 0;
		} else if (nsec < 0) {
			t.sec = (x$6 = t.sec, x$7 = new $Int64(0, 1), new $Int64(x$6.$high - x$7.$high, x$6.$low - x$7.$low));
			nsec = nsec + (1000000000) >> 0;
		}
		t.nsec = nsec;
		return t;
	};
	Time.prototype.Add = function(d) { return this.$val.Add(d); };
	Time.ptr.prototype.Sub = function(u) {
		var d, t, u, x, x$1, x$2, x$3, x$4;
		t = $clone(this, Time);
		u = $clone(u, Time);
		d = (x = $mul64((x$1 = (x$2 = t.sec, x$3 = u.sec, new $Int64(x$2.$high - x$3.$high, x$2.$low - x$3.$low)), new Duration(x$1.$high, x$1.$low)), new Duration(0, 1000000000)), x$4 = new Duration(0, (t.nsec - u.nsec >> 0)), new Duration(x.$high + x$4.$high, x.$low + x$4.$low));
		if (u.Add(d).Equal(t)) {
			return d;
		} else if (t.Before(u)) {
			return new Duration(-2147483648, 0);
		} else {
			return new Duration(2147483647, 4294967295);
		}
	};
	Time.prototype.Sub = function(u) { return this.$val.Sub(u); };
	Time.ptr.prototype.AddDate = function(years, months$1, days$1) {
		var _tuple$1, _tuple$2, day, days$1, hour, min, month, months$1, sec, t, year, years;
		t = $clone(this, Time);
		_tuple$1 = t.Date(); year = _tuple$1[0]; month = _tuple$1[1]; day = _tuple$1[2];
		_tuple$2 = t.Clock(); hour = _tuple$2[0]; min = _tuple$2[1]; sec = _tuple$2[2];
		return Date(year + years >> 0, month + (months$1 >> 0) >> 0, day + days$1 >> 0, hour, min, sec, (t.nsec >> 0), t.loc);
	};
	Time.prototype.AddDate = function(years, months$1, days$1) { return this.$val.AddDate(years, months$1, days$1); };
	Time.ptr.prototype.date = function(full) {
		var _tuple$1, day = 0, full, month = 0, t, yday = 0, year = 0;
		t = $clone(this, Time);
		_tuple$1 = absDate(t.abs(), full); year = _tuple$1[0]; month = _tuple$1[1]; day = _tuple$1[2]; yday = _tuple$1[3];
		return [year, month, day, yday];
	};
	Time.prototype.date = function(full) { return this.$val.date(full); };
	absDate = function(abs, full) {
		var _q, abs, begin, d, day = 0, end, full, month = 0, n, x, x$1, x$10, x$11, x$2, x$3, x$4, x$5, x$6, x$7, x$8, x$9, y, yday = 0, year = 0;
		d = $div64(abs, new $Uint64(0, 86400), false);
		n = $div64(d, new $Uint64(0, 146097), false);
		y = $mul64(new $Uint64(0, 400), n);
		d = (x = $mul64(new $Uint64(0, 146097), n), new $Uint64(d.$high - x.$high, d.$low - x.$low));
		n = $div64(d, new $Uint64(0, 36524), false);
		n = (x$1 = $shiftRightUint64(n, 2), new $Uint64(n.$high - x$1.$high, n.$low - x$1.$low));
		y = (x$2 = $mul64(new $Uint64(0, 100), n), new $Uint64(y.$high + x$2.$high, y.$low + x$2.$low));
		d = (x$3 = $mul64(new $Uint64(0, 36524), n), new $Uint64(d.$high - x$3.$high, d.$low - x$3.$low));
		n = $div64(d, new $Uint64(0, 1461), false);
		y = (x$4 = $mul64(new $Uint64(0, 4), n), new $Uint64(y.$high + x$4.$high, y.$low + x$4.$low));
		d = (x$5 = $mul64(new $Uint64(0, 1461), n), new $Uint64(d.$high - x$5.$high, d.$low - x$5.$low));
		n = $div64(d, new $Uint64(0, 365), false);
		n = (x$6 = $shiftRightUint64(n, 2), new $Uint64(n.$high - x$6.$high, n.$low - x$6.$low));
		y = (x$7 = n, new $Uint64(y.$high + x$7.$high, y.$low + x$7.$low));
		d = (x$8 = $mul64(new $Uint64(0, 365), n), new $Uint64(d.$high - x$8.$high, d.$low - x$8.$low));
		year = ((x$9 = (x$10 = new $Int64(y.$high, y.$low), new $Int64(x$10.$high + -69, x$10.$low + 4075721025)), x$9.$low + ((x$9.$high >> 31) * 4294967296)) >> 0);
		yday = (d.$low >> 0);
		if (!full) {
			return [year, month, day, yday];
		}
		day = yday;
		if (isLeap(year)) {
			if (day > 59) {
				day = day - (1) >> 0;
			} else if (day === 59) {
				month = 2;
				day = 29;
				return [year, month, day, yday];
			}
		}
		month = ((_q = day / 31, (_q === _q && _q !== 1/0 && _q !== -1/0) ? _q >> 0 : $throwRuntimeError("integer divide by zero")) >> 0);
		end = ((x$11 = month + 1 >> 0, ((x$11 < 0 || x$11 >= daysBefore.length) ? $throwRuntimeError("index out of range") : daysBefore[x$11])) >> 0);
		begin = 0;
		if (day >= end) {
			month = month + (1) >> 0;
			begin = end;
		} else {
			begin = (((month < 0 || month >= daysBefore.length) ? $throwRuntimeError("index out of range") : daysBefore[month]) >> 0);
		}
		month = month + (1) >> 0;
		day = (day - begin >> 0) + 1 >> 0;
		return [year, month, day, yday];
	};
	Now = $pkg.Now = function() {
		var _tuple$1, nsec, sec;
		_tuple$1 = now(); sec = _tuple$1[0]; nsec = _tuple$1[1];
		return new Time.ptr(new $Int64(sec.$high + 14, sec.$low + 2006054656), nsec, $pkg.Local);
	};
	Time.ptr.prototype.UTC = function() {
		var t;
		t = $clone(this, Time);
		t.loc = $pkg.UTC;
		return t;
	};
	Time.prototype.UTC = function() { return this.$val.UTC(); };
	Time.ptr.prototype.Local = function() {
		var t;
		t = $clone(this, Time);
		t.loc = $pkg.Local;
		return t;
	};
	Time.prototype.Local = function() { return this.$val.Local(); };
	Time.ptr.prototype.In = function(loc) {
		var loc, t;
		t = $clone(this, Time);
		if (loc === ptrType$3.nil) {
			$panic(new $String("time: missing Location in call to Time.In"));
		}
		t.loc = loc;
		return t;
	};
	Time.prototype.In = function(loc) { return this.$val.In(loc); };
	Time.ptr.prototype.Location = function() {
		var l, t;
		t = $clone(this, Time);
		l = t.loc;
		if (l === ptrType$2.nil) {
			l = $pkg.UTC;
		}
		return l;
	};
	Time.prototype.Location = function() { return this.$val.Location(); };
	Time.ptr.prototype.Zone = function() {
		var _tuple$1, name = "", offset = 0, t, x;
		t = $clone(this, Time);
		_tuple$1 = t.loc.lookup((x = t.sec, new $Int64(x.$high + -15, x.$low + 2288912640))); name = _tuple$1[0]; offset = _tuple$1[1];
		return [name, offset];
	};
	Time.prototype.Zone = function() { return this.$val.Zone(); };
	Time.ptr.prototype.Unix = function() {
		var t, x;
		t = $clone(this, Time);
		return (x = t.sec, new $Int64(x.$high + -15, x.$low + 2288912640));
	};
	Time.prototype.Unix = function() { return this.$val.Unix(); };
	Time.ptr.prototype.UnixNano = function() {
		var t, x, x$1, x$2;
		t = $clone(this, Time);
		return (x = $mul64(((x$1 = t.sec, new $Int64(x$1.$high + -15, x$1.$low + 2288912640))), new $Int64(0, 1000000000)), x$2 = new $Int64(0, t.nsec), new $Int64(x.$high + x$2.$high, x.$low + x$2.$low));
	};
	Time.prototype.UnixNano = function() { return this.$val.UnixNano(); };
	Time.ptr.prototype.MarshalBinary = function() {
		var _q, _r$1, _tuple$1, enc, offset, offsetMin, t;
		t = $clone(this, Time);
		offsetMin = 0;
		if (t.Location() === utcLoc) {
			offsetMin = -1;
		} else {
			_tuple$1 = t.Zone(); offset = _tuple$1[1];
			if (!(((_r$1 = offset % 60, _r$1 === _r$1 ? _r$1 : $throwRuntimeError("integer divide by zero")) === 0))) {
				return [sliceType$25.nil, errors.New("Time.MarshalBinary: zone offset has fractional minute")];
			}
			offset = (_q = offset / (60), (_q === _q && _q !== 1/0 && _q !== -1/0) ? _q >> 0 : $throwRuntimeError("integer divide by zero"));
			if (offset < -32768 || (offset === -1) || offset > 32767) {
				return [sliceType$25.nil, errors.New("Time.MarshalBinary: unexpected zone offset")];
			}
			offsetMin = (offset << 16 >> 16);
		}
		enc = new sliceType$26([1, ($shiftRightInt64(t.sec, 56).$low << 24 >>> 24), ($shiftRightInt64(t.sec, 48).$low << 24 >>> 24), ($shiftRightInt64(t.sec, 40).$low << 24 >>> 24), ($shiftRightInt64(t.sec, 32).$low << 24 >>> 24), ($shiftRightInt64(t.sec, 24).$low << 24 >>> 24), ($shiftRightInt64(t.sec, 16).$low << 24 >>> 24), ($shiftRightInt64(t.sec, 8).$low << 24 >>> 24), (t.sec.$low << 24 >>> 24), ((t.nsec >> 24 >> 0) << 24 >>> 24), ((t.nsec >> 16 >> 0) << 24 >>> 24), ((t.nsec >> 8 >> 0) << 24 >>> 24), (t.nsec << 24 >>> 24), ((offsetMin >> 8 << 16 >> 16) << 24 >>> 24), (offsetMin << 24 >>> 24)]);
		return [enc, $ifaceNil];
	};
	Time.prototype.MarshalBinary = function() { return this.$val.MarshalBinary(); };
	Time.ptr.prototype.UnmarshalBinary = function(data$1) {
		var _tuple$1, buf, data$1, localoff, offset, t, x, x$1, x$10, x$11, x$12, x$13, x$14, x$2, x$3, x$4, x$5, x$6, x$7, x$8, x$9;
		t = this;
		buf = data$1;
		if (buf.$length === 0) {
			return errors.New("Time.UnmarshalBinary: no data");
		}
		if (!(((0 >= buf.$length ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + 0]) === 1))) {
			return errors.New("Time.UnmarshalBinary: unsupported version");
		}
		if (!((buf.$length === 15))) {
			return errors.New("Time.UnmarshalBinary: invalid length");
		}
		buf = $subslice(buf, 1);
		t.sec = (x = (x$1 = (x$2 = (x$3 = (x$4 = (x$5 = (x$6 = new $Int64(0, (7 >= buf.$length ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + 7])), x$7 = $shiftLeft64(new $Int64(0, (6 >= buf.$length ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + 6])), 8), new $Int64(x$6.$high | x$7.$high, (x$6.$low | x$7.$low) >>> 0)), x$8 = $shiftLeft64(new $Int64(0, (5 >= buf.$length ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + 5])), 16), new $Int64(x$5.$high | x$8.$high, (x$5.$low | x$8.$low) >>> 0)), x$9 = $shiftLeft64(new $Int64(0, (4 >= buf.$length ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + 4])), 24), new $Int64(x$4.$high | x$9.$high, (x$4.$low | x$9.$low) >>> 0)), x$10 = $shiftLeft64(new $Int64(0, (3 >= buf.$length ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + 3])), 32), new $Int64(x$3.$high | x$10.$high, (x$3.$low | x$10.$low) >>> 0)), x$11 = $shiftLeft64(new $Int64(0, (2 >= buf.$length ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + 2])), 40), new $Int64(x$2.$high | x$11.$high, (x$2.$low | x$11.$low) >>> 0)), x$12 = $shiftLeft64(new $Int64(0, (1 >= buf.$length ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + 1])), 48), new $Int64(x$1.$high | x$12.$high, (x$1.$low | x$12.$low) >>> 0)), x$13 = $shiftLeft64(new $Int64(0, (0 >= buf.$length ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + 0])), 56), new $Int64(x.$high | x$13.$high, (x.$low | x$13.$low) >>> 0));
		buf = $subslice(buf, 8);
		t.nsec = ((((3 >= buf.$length ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + 3]) >> 0) | (((2 >= buf.$length ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + 2]) >> 0) << 8 >> 0)) | (((1 >= buf.$length ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + 1]) >> 0) << 16 >> 0)) | (((0 >= buf.$length ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + 0]) >> 0) << 24 >> 0);
		buf = $subslice(buf, 4);
		offset = ((((1 >= buf.$length ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + 1]) << 16 >> 16) | (((0 >= buf.$length ? $throwRuntimeError("index out of range") : buf.$array[buf.$offset + 0]) << 16 >> 16) << 8 << 16 >> 16)) >> 0) * 60 >> 0;
		if (offset === -60) {
			t.loc = utcLoc;
		} else {
			_tuple$1 = $pkg.Local.lookup((x$14 = t.sec, new $Int64(x$14.$high + -15, x$14.$low + 2288912640))); localoff = _tuple$1[1];
			if (offset === localoff) {
				t.loc = $pkg.Local;
			} else {
				t.loc = FixedZone("", offset);
			}
		}
		return $ifaceNil;
	};
	Time.prototype.UnmarshalBinary = function(data$1) { return this.$val.UnmarshalBinary(data$1); };
	Time.ptr.prototype.GobEncode = function() {
		var t;
		t = $clone(this, Time);
		return t.MarshalBinary();
	};
	Time.prototype.GobEncode = function() { return this.$val.GobEncode(); };
	Time.ptr.prototype.GobDecode = function(data$1) {
		var data$1, t;
		t = this;
		return t.UnmarshalBinary(data$1);
	};
	Time.prototype.GobDecode = function(data$1) { return this.$val.GobDecode(data$1); };
	Time.ptr.prototype.MarshalJSON = function() {
		var t, y;
		t = $clone(this, Time);
		y = t.Year();
		if (y < 0 || y >= 10000) {
			return [sliceType$27.nil, errors.New("Time.MarshalJSON: year outside of range [0,9999]")];
		}
		return [new sliceType$28($stringToBytes(t.Format("\"2006-01-02T15:04:05.999999999Z07:00\""))), $ifaceNil];
	};
	Time.prototype.MarshalJSON = function() { return this.$val.MarshalJSON(); };
	Time.ptr.prototype.UnmarshalJSON = function(data$1) {
		var _tuple$1, data$1, err = $ifaceNil, t;
		t = this;
		_tuple$1 = Parse("\"2006-01-02T15:04:05Z07:00\"", $bytesToString(data$1)); $copy(t, _tuple$1[0], Time); err = _tuple$1[1];
		return err;
	};
	Time.prototype.UnmarshalJSON = function(data$1) { return this.$val.UnmarshalJSON(data$1); };
	Time.ptr.prototype.MarshalText = function() {
		var t, y;
		t = $clone(this, Time);
		y = t.Year();
		if (y < 0 || y >= 10000) {
			return [sliceType$29.nil, errors.New("Time.MarshalText: year outside of range [0,9999]")];
		}
		return [new sliceType$30($stringToBytes(t.Format("2006-01-02T15:04:05.999999999Z07:00"))), $ifaceNil];
	};
	Time.prototype.MarshalText = function() { return this.$val.MarshalText(); };
	Time.ptr.prototype.UnmarshalText = function(data$1) {
		var _tuple$1, data$1, err = $ifaceNil, t;
		t = this;
		_tuple$1 = Parse("2006-01-02T15:04:05Z07:00", $bytesToString(data$1)); $copy(t, _tuple$1[0], Time); err = _tuple$1[1];
		return err;
	};
	Time.prototype.UnmarshalText = function(data$1) { return this.$val.UnmarshalText(data$1); };
	isLeap = function(year) {
		var _r$1, _r$2, _r$3, year;
		return ((_r$1 = year % 4, _r$1 === _r$1 ? _r$1 : $throwRuntimeError("integer divide by zero")) === 0) && (!(((_r$2 = year % 100, _r$2 === _r$2 ? _r$2 : $throwRuntimeError("integer divide by zero")) === 0)) || ((_r$3 = year % 400, _r$3 === _r$3 ? _r$3 : $throwRuntimeError("integer divide by zero")) === 0));
	};
	norm = function(hi, lo, base) {
		var _q, _q$1, _tmp, _tmp$1, base, hi, lo, n, n$1, nhi = 0, nlo = 0;
		if (lo < 0) {
			n = (_q = ((-lo - 1 >> 0)) / base, (_q === _q && _q !== 1/0 && _q !== -1/0) ? _q >> 0 : $throwRuntimeError("integer divide by zero")) + 1 >> 0;
			hi = hi - (n) >> 0;
			lo = lo + ((n * base >> 0)) >> 0;
		}
		if (lo >= base) {
			n$1 = (_q$1 = lo / base, (_q$1 === _q$1 && _q$1 !== 1/0 && _q$1 !== -1/0) ? _q$1 >> 0 : $throwRuntimeError("integer divide by zero"));
			hi = hi + (n$1) >> 0;
			lo = lo - ((n$1 * base >> 0)) >> 0;
		}
		_tmp = hi; _tmp$1 = lo; nhi = _tmp; nlo = _tmp$1;
		return [nhi, nlo];
	};
	Date = $pkg.Date = function(year, month, day, hour, min, sec, nsec, loc) {
		var _tuple$1, _tuple$2, _tuple$3, _tuple$4, _tuple$5, _tuple$6, _tuple$7, _tuple$8, abs, d, day, end, hour, loc, m, min, month, n, nsec, offset, sec, start, unix, utc, x, x$1, x$10, x$11, x$12, x$13, x$14, x$15, x$2, x$3, x$4, x$5, x$6, x$7, x$8, x$9, y, year;
		if (loc === ptrType$4.nil) {
			$panic(new $String("time: missing Location in call to Date"));
		}
		m = (month >> 0) - 1 >> 0;
		_tuple$1 = norm(year, m, 12); year = _tuple$1[0]; m = _tuple$1[1];
		month = (m >> 0) + 1 >> 0;
		_tuple$2 = norm(sec, nsec, 1000000000); sec = _tuple$2[0]; nsec = _tuple$2[1];
		_tuple$3 = norm(min, sec, 60); min = _tuple$3[0]; sec = _tuple$3[1];
		_tuple$4 = norm(hour, min, 60); hour = _tuple$4[0]; min = _tuple$4[1];
		_tuple$5 = norm(day, hour, 24); day = _tuple$5[0]; hour = _tuple$5[1];
		y = (x = (x$1 = new $Int64(0, year), new $Int64(x$1.$high - -69, x$1.$low - 4075721025)), new $Uint64(x.$high, x.$low));
		n = $div64(y, new $Uint64(0, 400), false);
		y = (x$2 = $mul64(new $Uint64(0, 400), n), new $Uint64(y.$high - x$2.$high, y.$low - x$2.$low));
		d = $mul64(new $Uint64(0, 146097), n);
		n = $div64(y, new $Uint64(0, 100), false);
		y = (x$3 = $mul64(new $Uint64(0, 100), n), new $Uint64(y.$high - x$3.$high, y.$low - x$3.$low));
		d = (x$4 = $mul64(new $Uint64(0, 36524), n), new $Uint64(d.$high + x$4.$high, d.$low + x$4.$low));
		n = $div64(y, new $Uint64(0, 4), false);
		y = (x$5 = $mul64(new $Uint64(0, 4), n), new $Uint64(y.$high - x$5.$high, y.$low - x$5.$low));
		d = (x$6 = $mul64(new $Uint64(0, 1461), n), new $Uint64(d.$high + x$6.$high, d.$low + x$6.$low));
		n = y;
		d = (x$7 = $mul64(new $Uint64(0, 365), n), new $Uint64(d.$high + x$7.$high, d.$low + x$7.$low));
		d = (x$8 = new $Uint64(0, (x$9 = month - 1 >> 0, ((x$9 < 0 || x$9 >= daysBefore.length) ? $throwRuntimeError("index out of range") : daysBefore[x$9]))), new $Uint64(d.$high + x$8.$high, d.$low + x$8.$low));
		if (isLeap(year) && month >= 3) {
			d = (x$10 = new $Uint64(0, 1), new $Uint64(d.$high + x$10.$high, d.$low + x$10.$low));
		}
		d = (x$11 = new $Uint64(0, (day - 1 >> 0)), new $Uint64(d.$high + x$11.$high, d.$low + x$11.$low));
		abs = $mul64(d, new $Uint64(0, 86400));
		abs = (x$12 = new $Uint64(0, (((hour * 3600 >> 0) + (min * 60 >> 0) >> 0) + sec >> 0)), new $Uint64(abs.$high + x$12.$high, abs.$low + x$12.$low));
		unix = (x$13 = new $Int64(abs.$high, abs.$low), new $Int64(x$13.$high + -2147483647, x$13.$low + 3844486912));
		_tuple$6 = loc.lookup(unix); offset = _tuple$6[1]; start = _tuple$6[3]; end = _tuple$6[4];
		if (!((offset === 0))) {
			utc = (x$14 = new $Int64(0, offset), new $Int64(unix.$high - x$14.$high, unix.$low - x$14.$low));
			if ((utc.$high < start.$high || (utc.$high === start.$high && utc.$low < start.$low))) {
				_tuple$7 = loc.lookup(new $Int64(start.$high - 0, start.$low - 1)); offset = _tuple$7[1];
			} else if ((utc.$high > end.$high || (utc.$high === end.$high && utc.$low >= end.$low))) {
				_tuple$8 = loc.lookup(end); offset = _tuple$8[1];
			}
			unix = (x$15 = new $Int64(0, offset), new $Int64(unix.$high - x$15.$high, unix.$low - x$15.$low));
		}
		return new Time.ptr(new $Int64(unix.$high + 14, unix.$low + 2006054656), (nsec >> 0), loc);
	};
	Time.ptr.prototype.Truncate = function(d) {
		var _tuple$1, d, r, t;
		t = $clone(this, Time);
		if ((d.$high < 0 || (d.$high === 0 && d.$low <= 0))) {
			return t;
		}
		_tuple$1 = div(t, d); r = _tuple$1[1];
		return t.Add(new Duration(-r.$high, -r.$low));
	};
	Time.prototype.Truncate = function(d) { return this.$val.Truncate(d); };
	Time.ptr.prototype.Round = function(d) {
		var _tuple$1, d, r, t, x;
		t = $clone(this, Time);
		if ((d.$high < 0 || (d.$high === 0 && d.$low <= 0))) {
			return t;
		}
		_tuple$1 = div(t, d); r = _tuple$1[1];
		if ((x = new Duration(r.$high + r.$high, r.$low + r.$low), (x.$high < d.$high || (x.$high === d.$high && x.$low < d.$low)))) {
			return t.Add(new Duration(-r.$high, -r.$low));
		}
		return t.Add(new Duration(d.$high - r.$high, d.$low - r.$low));
	};
	Time.prototype.Round = function(d) { return this.$val.Round(d); };
	div = function(t, d) {
		var _q, _r$1, _tmp, _tmp$1, _tmp$2, _tmp$3, _tmp$4, _tmp$5, d, d0, d1, d1$1, neg, nsec, qmod2 = 0, r = new Duration(0, 0), sec, t, tmp, u0, u0x, u1, x, x$1, x$10, x$11, x$12, x$13, x$14, x$15, x$16, x$17, x$18, x$19, x$2, x$3, x$4, x$5, x$6, x$7, x$8, x$9;
		t = $clone(t, Time);
		neg = false;
		nsec = t.nsec;
		if ((x = t.sec, (x.$high < 0 || (x.$high === 0 && x.$low < 0)))) {
			neg = true;
			t.sec = (x$1 = t.sec, new $Int64(-x$1.$high, -x$1.$low));
			nsec = -nsec;
			if (nsec < 0) {
				nsec = nsec + (1000000000) >> 0;
				t.sec = (x$2 = t.sec, x$3 = new $Int64(0, 1), new $Int64(x$2.$high - x$3.$high, x$2.$low - x$3.$low));
			}
		}
		if ((d.$high < 0 || (d.$high === 0 && d.$low < 1000000000)) && (x$4 = $div64(new Duration(0, 1000000000), (new Duration(d.$high + d.$high, d.$low + d.$low)), true), (x$4.$high === 0 && x$4.$low === 0))) {
			qmod2 = ((_q = nsec / ((d.$low + ((d.$high >> 31) * 4294967296)) >> 0), (_q === _q && _q !== 1/0 && _q !== -1/0) ? _q >> 0 : $throwRuntimeError("integer divide by zero")) >> 0) & 1;
			r = new Duration(0, (_r$1 = nsec % ((d.$low + ((d.$high >> 31) * 4294967296)) >> 0), _r$1 === _r$1 ? _r$1 : $throwRuntimeError("integer divide by zero")));
		} else if ((x$5 = $div64(d, new Duration(0, 1000000000), true), (x$5.$high === 0 && x$5.$low === 0))) {
			d1 = (x$6 = $div64(d, new Duration(0, 1000000000), false), new $Int64(x$6.$high, x$6.$low));
			qmod2 = ((x$7 = $div64(t.sec, d1, false), x$7.$low + ((x$7.$high >> 31) * 4294967296)) >> 0) & 1;
			r = (x$8 = $mul64((x$9 = $div64(t.sec, d1, true), new Duration(x$9.$high, x$9.$low)), new Duration(0, 1000000000)), x$10 = new Duration(0, nsec), new Duration(x$8.$high + x$10.$high, x$8.$low + x$10.$low));
		} else {
			sec = (x$11 = t.sec, new $Uint64(x$11.$high, x$11.$low));
			tmp = $mul64(($shiftRightUint64(sec, 32)), new $Uint64(0, 1000000000));
			u1 = $shiftRightUint64(tmp, 32);
			u0 = $shiftLeft64(tmp, 32);
			tmp = $mul64(new $Uint64(sec.$high & 0, (sec.$low & 4294967295) >>> 0), new $Uint64(0, 1000000000));
			_tmp = u0; _tmp$1 = new $Uint64(u0.$high + tmp.$high, u0.$low + tmp.$low); u0x = _tmp; u0 = _tmp$1;
			if ((u0.$high < u0x.$high || (u0.$high === u0x.$high && u0.$low < u0x.$low))) {
				u1 = (x$12 = new $Uint64(0, 1), new $Uint64(u1.$high + x$12.$high, u1.$low + x$12.$low));
			}
			_tmp$2 = u0; _tmp$3 = (x$13 = new $Uint64(0, nsec), new $Uint64(u0.$high + x$13.$high, u0.$low + x$13.$low)); u0x = _tmp$2; u0 = _tmp$3;
			if ((u0.$high < u0x.$high || (u0.$high === u0x.$high && u0.$low < u0x.$low))) {
				u1 = (x$14 = new $Uint64(0, 1), new $Uint64(u1.$high + x$14.$high, u1.$low + x$14.$low));
			}
			d1$1 = new $Uint64(d.$high, d.$low);
			while (true) {
				if (!(!((x$15 = $shiftRightUint64(d1$1, 63), (x$15.$high === 0 && x$15.$low === 1))))) { break; }
				d1$1 = $shiftLeft64(d1$1, (1));
			}
			d0 = new $Uint64(0, 0);
			while (true) {
				qmod2 = 0;
				if ((u1.$high > d1$1.$high || (u1.$high === d1$1.$high && u1.$low > d1$1.$low)) || (u1.$high === d1$1.$high && u1.$low === d1$1.$low) && (u0.$high > d0.$high || (u0.$high === d0.$high && u0.$low >= d0.$low))) {
					qmod2 = 1;
					_tmp$4 = u0; _tmp$5 = new $Uint64(u0.$high - d0.$high, u0.$low - d0.$low); u0x = _tmp$4; u0 = _tmp$5;
					if ((u0.$high > u0x.$high || (u0.$high === u0x.$high && u0.$low > u0x.$low))) {
						u1 = (x$16 = new $Uint64(0, 1), new $Uint64(u1.$high - x$16.$high, u1.$low - x$16.$low));
					}
					u1 = (x$17 = d1$1, new $Uint64(u1.$high - x$17.$high, u1.$low - x$17.$low));
				}
				if ((d1$1.$high === 0 && d1$1.$low === 0) && (x$18 = new $Uint64(d.$high, d.$low), (d0.$high === x$18.$high && d0.$low === x$18.$low))) {
					break;
				}
				d0 = $shiftRightUint64(d0, (1));
				d0 = (x$19 = $shiftLeft64((new $Uint64(d1$1.$high & 0, (d1$1.$low & 1) >>> 0)), 63), new $Uint64(d0.$high | x$19.$high, (d0.$low | x$19.$low) >>> 0));
				d1$1 = $shiftRightUint64(d1$1, (1));
			}
			r = new Duration(u0.$high, u0.$low);
		}
		if (neg && !((r.$high === 0 && r.$low === 0))) {
			qmod2 = (qmod2 ^ (1)) >> 0;
			r = new Duration(d.$high - r.$high, d.$low - r.$low);
		}
		return [qmod2, r];
	};
	Location.ptr.prototype.get = function() {
		var l;
		l = this;
		if (l === ptrType$5.nil) {
			return utcLoc;
		}
		if (l === localLoc) {
			localOnce.Do(initLocal);
		}
		return l;
	};
	Location.prototype.get = function() { return this.$val.get(); };
	Location.ptr.prototype.String = function() {
		var l;
		l = this;
		return l.get().name;
	};
	Location.prototype.String = function() { return this.$val.String(); };
	FixedZone = $pkg.FixedZone = function(name, offset) {
		var l, name, offset, x;
		l = new Location.ptr(name, new sliceType$31([new zone.ptr(name, offset, false)]), new sliceType$32([new zoneTrans.ptr(new $Int64(-2147483648, 0), 0, false, false)]), new $Int64(-2147483648, 0), new $Int64(2147483647, 4294967295), ptrType.nil);
		l.cacheZone = (x = l.zone, (0 >= x.$length ? $throwRuntimeError("index out of range") : x.$array[x.$offset + 0]));
		return l;
	};
	Location.ptr.prototype.lookup = function(sec) {
		var _q, end = new $Int64(0, 0), hi, isDST = false, l, lim, lo, m, name = "", offset = 0, sec, start = new $Int64(0, 0), tx, x, x$1, x$2, x$3, x$4, x$5, x$6, x$7, x$8, zone$1, zone$2, zone$3;
		l = this;
		l = l.get();
		if (l.zone.$length === 0) {
			name = "UTC";
			offset = 0;
			isDST = false;
			start = new $Int64(-2147483648, 0);
			end = new $Int64(2147483647, 4294967295);
			return [name, offset, isDST, start, end];
		}
		zone$1 = l.cacheZone;
		if (!(zone$1 === ptrType.nil) && (x = l.cacheStart, (x.$high < sec.$high || (x.$high === sec.$high && x.$low <= sec.$low))) && (x$1 = l.cacheEnd, (sec.$high < x$1.$high || (sec.$high === x$1.$high && sec.$low < x$1.$low)))) {
			name = zone$1.name;
			offset = zone$1.offset;
			isDST = zone$1.isDST;
			start = l.cacheStart;
			end = l.cacheEnd;
			return [name, offset, isDST, start, end];
		}
		if ((l.tx.$length === 0) || (x$2 = (x$3 = l.tx, (0 >= x$3.$length ? $throwRuntimeError("index out of range") : x$3.$array[x$3.$offset + 0])).when, (sec.$high < x$2.$high || (sec.$high === x$2.$high && sec.$low < x$2.$low)))) {
			zone$2 = (x$4 = l.zone, x$5 = l.lookupFirstZone(), ((x$5 < 0 || x$5 >= x$4.$length) ? $throwRuntimeError("index out of range") : x$4.$array[x$4.$offset + x$5]));
			name = zone$2.name;
			offset = zone$2.offset;
			isDST = zone$2.isDST;
			start = new $Int64(-2147483648, 0);
			if (l.tx.$length > 0) {
				end = (x$6 = l.tx, (0 >= x$6.$length ? $throwRuntimeError("index out of range") : x$6.$array[x$6.$offset + 0])).when;
			} else {
				end = new $Int64(2147483647, 4294967295);
			}
			return [name, offset, isDST, start, end];
		}
		tx = l.tx;
		end = new $Int64(2147483647, 4294967295);
		lo = 0;
		hi = tx.$length;
		while (true) {
			if (!((hi - lo >> 0) > 1)) { break; }
			m = lo + (_q = ((hi - lo >> 0)) / 2, (_q === _q && _q !== 1/0 && _q !== -1/0) ? _q >> 0 : $throwRuntimeError("integer divide by zero")) >> 0;
			lim = ((m < 0 || m >= tx.$length) ? $throwRuntimeError("index out of range") : tx.$array[tx.$offset + m]).when;
			if ((sec.$high < lim.$high || (sec.$high === lim.$high && sec.$low < lim.$low))) {
				end = lim;
				hi = m;
			} else {
				lo = m;
			}
		}
		zone$3 = (x$7 = l.zone, x$8 = ((lo < 0 || lo >= tx.$length) ? $throwRuntimeError("index out of range") : tx.$array[tx.$offset + lo]).index, ((x$8 < 0 || x$8 >= x$7.$length) ? $throwRuntimeError("index out of range") : x$7.$array[x$7.$offset + x$8]));
		name = zone$3.name;
		offset = zone$3.offset;
		isDST = zone$3.isDST;
		start = ((lo < 0 || lo >= tx.$length) ? $throwRuntimeError("index out of range") : tx.$array[tx.$offset + lo]).when;
		return [name, offset, isDST, start, end];
	};
	Location.prototype.lookup = function(sec) { return this.$val.lookup(sec); };
	Location.ptr.prototype.lookupFirstZone = function() {
		var _i, _ref, l, x, x$1, x$2, x$3, x$4, x$5, zi, zi$1;
		l = this;
		if (!l.firstZoneUsed()) {
			return 0;
		}
		if (l.tx.$length > 0 && (x = l.zone, x$1 = (x$2 = l.tx, (0 >= x$2.$length ? $throwRuntimeError("index out of range") : x$2.$array[x$2.$offset + 0])).index, ((x$1 < 0 || x$1 >= x.$length) ? $throwRuntimeError("index out of range") : x.$array[x.$offset + x$1])).isDST) {
			zi = ((x$3 = l.tx, (0 >= x$3.$length ? $throwRuntimeError("index out of range") : x$3.$array[x$3.$offset + 0])).index >> 0) - 1 >> 0;
			while (true) {
				if (!(zi >= 0)) { break; }
				if (!(x$4 = l.zone, ((zi < 0 || zi >= x$4.$length) ? $throwRuntimeError("index out of range") : x$4.$array[x$4.$offset + zi])).isDST) {
					return zi;
				}
				zi = zi - (1) >> 0;
			}
		}
		_ref = l.zone;
		_i = 0;
		while (true) {
			if (!(_i < _ref.$length)) { break; }
			zi$1 = _i;
			if (!(x$5 = l.zone, ((zi$1 < 0 || zi$1 >= x$5.$length) ? $throwRuntimeError("index out of range") : x$5.$array[x$5.$offset + zi$1])).isDST) {
				return zi$1;
			}
			_i++;
		}
		return 0;
	};
	Location.prototype.lookupFirstZone = function() { return this.$val.lookupFirstZone(); };
	Location.ptr.prototype.firstZoneUsed = function() {
		var _i, _ref, l, tx;
		l = this;
		_ref = l.tx;
		_i = 0;
		while (true) {
			if (!(_i < _ref.$length)) { break; }
			tx = $clone(((_i < 0 || _i >= _ref.$length) ? $throwRuntimeError("index out of range") : _ref.$array[_ref.$offset + _i]), zoneTrans);
			if (tx.index === 0) {
				return true;
			}
			_i++;
		}
		return false;
	};
	Location.prototype.firstZoneUsed = function() { return this.$val.firstZoneUsed(); };
	Location.ptr.prototype.lookupName = function(name, unix) {
		var _i, _i$1, _ref, _ref$1, _tmp, _tmp$1, _tmp$2, _tmp$3, _tmp$4, _tmp$5, _tuple$1, i, i$1, isDST = false, isDST$1, l, nam, name, offset = 0, offset$1, ok = false, unix, x, x$1, x$2, zone$1, zone$2;
		l = this;
		l = l.get();
		_ref = l.zone;
		_i = 0;
		while (true) {
			if (!(_i < _ref.$length)) { break; }
			i = _i;
			zone$1 = (x = l.zone, ((i < 0 || i >= x.$length) ? $throwRuntimeError("index out of range") : x.$array[x.$offset + i]));
			if (zone$1.name === name) {
				_tuple$1 = l.lookup((x$1 = new $Int64(0, zone$1.offset), new $Int64(unix.$high - x$1.$high, unix.$low - x$1.$low))); nam = _tuple$1[0]; offset$1 = _tuple$1[1]; isDST$1 = _tuple$1[2];
				if (nam === zone$1.name) {
					_tmp = offset$1; _tmp$1 = isDST$1; _tmp$2 = true; offset = _tmp; isDST = _tmp$1; ok = _tmp$2;
					return [offset, isDST, ok];
				}
			}
			_i++;
		}
		_ref$1 = l.zone;
		_i$1 = 0;
		while (true) {
			if (!(_i$1 < _ref$1.$length)) { break; }
			i$1 = _i$1;
			zone$2 = (x$2 = l.zone, ((i$1 < 0 || i$1 >= x$2.$length) ? $throwRuntimeError("index out of range") : x$2.$array[x$2.$offset + i$1]));
			if (zone$2.name === name) {
				_tmp$3 = zone$2.offset; _tmp$4 = zone$2.isDST; _tmp$5 = true; offset = _tmp$3; isDST = _tmp$4; ok = _tmp$5;
				return [offset, isDST, ok];
			}
			_i$1++;
		}
		return [offset, isDST, ok];
	};
	Location.prototype.lookupName = function(name, unix) { return this.$val.lookupName(name, unix); };
	ptrType$11.methods = [{prop: "Error", name: "Error", pkg: "", typ: $funcType([], [$String], false)}];
	Time.methods = [{prop: "String", name: "String", pkg: "", typ: $funcType([], [$String], false)}, {prop: "Format", name: "Format", pkg: "", typ: $funcType([$String], [$String], false)}, {prop: "After", name: "After", pkg: "", typ: $funcType([Time], [$Bool], false)}, {prop: "Before", name: "Before", pkg: "", typ: $funcType([Time], [$Bool], false)}, {prop: "Equal", name: "Equal", pkg: "", typ: $funcType([Time], [$Bool], false)}, {prop: "IsZero", name: "IsZero", pkg: "", typ: $funcType([], [$Bool], false)}, {prop: "abs", name: "abs", pkg: "time", typ: $funcType([], [$Uint64], false)}, {prop: "locabs", name: "locabs", pkg: "time", typ: $funcType([], [$String, $Int, $Uint64], false)}, {prop: "Date", name: "Date", pkg: "", typ: $funcType([], [$Int, Month, $Int], false)}, {prop: "Year", name: "Year", pkg: "", typ: $funcType([], [$Int], false)}, {prop: "Month", name: "Month", pkg: "", typ: $funcType([], [Month], false)}, {prop: "Day", name: "Day", pkg: "", typ: $funcType([], [$Int], false)}, {prop: "Weekday", name: "Weekday", pkg: "", typ: $funcType([], [Weekday], false)}, {prop: "ISOWeek", name: "ISOWeek", pkg: "", typ: $funcType([], [$Int, $Int], false)}, {prop: "Clock", name: "Clock", pkg: "", typ: $funcType([], [$Int, $Int, $Int], false)}, {prop: "Hour", name: "Hour", pkg: "", typ: $funcType([], [$Int], false)}, {prop: "Minute", name: "Minute", pkg: "", typ: $funcType([], [$Int], false)}, {prop: "Second", name: "Second", pkg: "", typ: $funcType([], [$Int], false)}, {prop: "Nanosecond", name: "Nanosecond", pkg: "", typ: $funcType([], [$Int], false)}, {prop: "YearDay", name: "YearDay", pkg: "", typ: $funcType([], [$Int], false)}, {prop: "Add", name: "Add", pkg: "", typ: $funcType([Duration], [Time], false)}, {prop: "Sub", name: "Sub", pkg: "", typ: $funcType([Time], [Duration], false)}, {prop: "AddDate", name: "AddDate", pkg: "", typ: $funcType([$Int, $Int, $Int], [Time], false)}, {prop: "date", name: "date", pkg: "time", typ: $funcType([$Bool], [$Int, Month, $Int, $Int], false)}, {prop: "UTC", name: "UTC", pkg: "", typ: $funcType([], [Time], false)}, {prop: "Local", name: "Local", pkg: "", typ: $funcType([], [Time], false)}, {prop: "In", name: "In", pkg: "", typ: $funcType([ptrType$3], [Time], false)}, {prop: "Location", name: "Location", pkg: "", typ: $funcType([], [ptrType$14], false)}, {prop: "Zone", name: "Zone", pkg: "", typ: $funcType([], [$String, $Int], false)}, {prop: "Unix", name: "Unix", pkg: "", typ: $funcType([], [$Int64], false)}, {prop: "UnixNano", name: "UnixNano", pkg: "", typ: $funcType([], [$Int64], false)}, {prop: "MarshalBinary", name: "MarshalBinary", pkg: "", typ: $funcType([], [sliceType$25, $error], false)}, {prop: "GobEncode", name: "GobEncode", pkg: "", typ: $funcType([], [sliceType$44, $error], false)}, {prop: "MarshalJSON", name: "MarshalJSON", pkg: "", typ: $funcType([], [sliceType$27, $error], false)}, {prop: "MarshalText", name: "MarshalText", pkg: "", typ: $funcType([], [sliceType$29, $error], false)}, {prop: "Truncate", name: "Truncate", pkg: "", typ: $funcType([Duration], [Time], false)}, {prop: "Round", name: "Round", pkg: "", typ: $funcType([Duration], [Time], false)}];
	ptrType$15.methods = [{prop: "UnmarshalBinary", name: "UnmarshalBinary", pkg: "", typ: $funcType([sliceType$43], [$error], false)}, {prop: "GobDecode", name: "GobDecode", pkg: "", typ: $funcType([sliceType$45], [$error], false)}, {prop: "UnmarshalJSON", name: "UnmarshalJSON", pkg: "", typ: $funcType([sliceType$46], [$error], false)}, {prop: "UnmarshalText", name: "UnmarshalText", pkg: "", typ: $funcType([sliceType$47], [$error], false)}];
	Month.methods = [{prop: "String", name: "String", pkg: "", typ: $funcType([], [$String], false)}];
	Weekday.methods = [{prop: "String", name: "String", pkg: "", typ: $funcType([], [$String], false)}];
	Duration.methods = [{prop: "String", name: "String", pkg: "", typ: $funcType([], [$String], false)}, {prop: "Nanoseconds", name: "Nanoseconds", pkg: "", typ: $funcType([], [$Int64], false)}, {prop: "Seconds", name: "Seconds", pkg: "", typ: $funcType([], [$Float64], false)}, {prop: "Minutes", name: "Minutes", pkg: "", typ: $funcType([], [$Float64], false)}, {prop: "Hours", name: "Hours", pkg: "", typ: $funcType([], [$Float64], false)}];
	ptrType$17.methods = [{prop: "get", name: "get", pkg: "time", typ: $funcType([], [ptrType$16], false)}, {prop: "String", name: "String", pkg: "", typ: $funcType([], [$String], false)}, {prop: "lookup", name: "lookup", pkg: "time", typ: $funcType([$Int64], [$String, $Int, $Bool, $Int64, $Int64], false)}, {prop: "lookupFirstZone", name: "lookupFirstZone", pkg: "time", typ: $funcType([], [$Int], false)}, {prop: "firstZoneUsed", name: "firstZoneUsed", pkg: "time", typ: $funcType([], [$Bool], false)}, {prop: "lookupName", name: "lookupName", pkg: "time", typ: $funcType([$String, $Int64], [$Int, $Bool, $Bool], false)}];
	ParseError.init([{prop: "Layout", name: "Layout", pkg: "", typ: $String, tag: ""}, {prop: "Value", name: "Value", pkg: "", typ: $String, tag: ""}, {prop: "LayoutElem", name: "LayoutElem", pkg: "", typ: $String, tag: ""}, {prop: "ValueElem", name: "ValueElem", pkg: "", typ: $String, tag: ""}, {prop: "Message", name: "Message", pkg: "", typ: $String, tag: ""}]);
	Time.init([{prop: "sec", name: "sec", pkg: "time", typ: $Int64, tag: ""}, {prop: "nsec", name: "nsec", pkg: "time", typ: $Int32, tag: ""}, {prop: "loc", name: "loc", pkg: "time", typ: ptrType$2, tag: ""}]);
	Location.init([{prop: "name", name: "name", pkg: "time", typ: $String, tag: ""}, {prop: "zone", name: "zone", pkg: "time", typ: sliceType$4, tag: ""}, {prop: "tx", name: "tx", pkg: "time", typ: sliceType$5, tag: ""}, {prop: "cacheStart", name: "cacheStart", pkg: "time", typ: $Int64, tag: ""}, {prop: "cacheEnd", name: "cacheEnd", pkg: "time", typ: $Int64, tag: ""}, {prop: "cacheZone", name: "cacheZone", pkg: "time", typ: ptrType, tag: ""}]);
	zone.init([{prop: "name", name: "name", pkg: "time", typ: $String, tag: ""}, {prop: "offset", name: "offset", pkg: "time", typ: $Int, tag: ""}, {prop: "isDST", name: "isDST", pkg: "time", typ: $Bool, tag: ""}]);
	zoneTrans.init([{prop: "when", name: "when", pkg: "time", typ: $Int64, tag: ""}, {prop: "index", name: "index", pkg: "time", typ: $Uint8, tag: ""}, {prop: "isstd", name: "isstd", pkg: "time", typ: $Bool, tag: ""}, {prop: "isutc", name: "isutc", pkg: "time", typ: $Bool, tag: ""}]);
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_time = function() { while (true) { switch ($s) { case 0:
		$r = errors.$init($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		$r = js.$init($BLOCKING); /* */ $s = 2; case 2: if ($r && $r.$blocking) { $r = $r(); }
		$r = nosync.$init($BLOCKING); /* */ $s = 3; case 3: if ($r && $r.$blocking) { $r = $r(); }
		$r = runtime.$init($BLOCKING); /* */ $s = 4; case 4: if ($r && $r.$blocking) { $r = $r(); }
		$r = strings.$init($BLOCKING); /* */ $s = 5; case 5: if ($r && $r.$blocking) { $r = $r(); }
		$r = syscall.$init($BLOCKING); /* */ $s = 6; case 6: if ($r && $r.$blocking) { $r = $r(); }
		localLoc = new Location.ptr();
		localOnce = new nosync.Once.ptr();
		std0x = $toNativeArray($kindInt, [260, 265, 524, 526, 528, 274]);
		longDayNames = new sliceType(["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"]);
		shortDayNames = new sliceType$1(["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"]);
		shortMonthNames = new sliceType$2(["---", "Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"]);
		longMonthNames = new sliceType$3(["---", "January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"]);
		atoiError = errors.New("time: invalid number");
		errBad = errors.New("bad value for field");
		errLeadingInt = errors.New("time: bad [0-9]*");
		months = $toNativeArray($kindString, ["January", "February", "March", "April", "May", "June", "July", "August", "September", "October", "November", "December"]);
		days = $toNativeArray($kindString, ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"]);
		daysBefore = $toNativeArray($kindInt32, [0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334, 365]);
		utcLoc = new Location.ptr("UTC", sliceType$4.nil, sliceType$5.nil, new $Int64(0, 0), new $Int64(0, 0), ptrType.nil);
		$pkg.UTC = utcLoc;
		$pkg.Local = localLoc;
		_r = syscall.Getenv("ZONEINFO", $BLOCKING); /* */ $s = 7; case 7: if (_r && _r.$blocking) { _r = _r(); }
		_tuple = _r; zoneinfo = _tuple[0];
		badData = errors.New("malformed time zone information");
		zoneDirs = new sliceType$6(["/usr/share/zoneinfo/", "/usr/share/lib/zoneinfo/", "/usr/lib/locale/TZ/", runtime.GOROOT() + "/lib/time/zoneinfo.zip"]);
		/* */ } return; } }; $init_time.$blocking = true; return $init_time;
	};
	return $pkg;
})();
$packages["main"] = (function() {
	var $pkg = {}, jquery, js, qunit, strconv, strings, time, Object, EvtScenario, working, sliceType, funcType, sliceType$2, funcType$1, sliceType$3, funcType$2, funcType$3, sliceType$4, arrayType, sliceType$5, mapType, ptrType, funcType$4, mapType$1, sliceType$6, mapType$2, sliceType$7, funcType$5, sliceType$8, sliceType$9, funcType$6, sliceType$10, funcType$7, sliceType$11, sliceType$12, sliceType$13, funcType$8, sliceType$14, sliceType$15, sliceType$16, sliceType$17, funcType$9, sliceType$18, sliceType$19, mapType$3, sliceType$20, sliceType$21, sliceType$22, mapType$4, sliceType$23, funcType$10, sliceType$24, mapType$5, funcType$11, sliceType$25, funcType$12, mapType$6, funcType$13, sliceType$26, funcType$14, funcType$15, sliceType$27, funcType$16, sliceType$28, sliceType$29, funcType$17, funcType$18, funcType$19, funcType$20, funcType$21, funcType$22, sliceType$30, funcType$23, sliceType$31, funcType$24, sliceType$32, funcType$25, mapType$7, sliceType$33, funcType$26, sliceType$34, funcType$27, funcType$28, funcType$29, funcType$30, sliceType$35, funcType$31, mapType$8, funcType$32, sliceType$36, funcType$33, mapType$9, sliceType$37, funcType$34, funcType$35, funcType$36, sliceType$38, funcType$37, sliceType$39, funcType$38, funcType$39, funcType$40, sliceType$40, funcType$41, funcType$42, sliceType$41, funcType$43, funcType$44, funcType$45, funcType$46, funcType$47, funcType$48, funcType$49, funcType$50, funcType$51, funcType$52, jQuery, countJohn, countKarl, getDocumentBody, stringify, NewWorking, asyncEvent, main;
	jquery = $packages["github.com/albrow/jquery"];
	js = $packages["github.com/gopherjs/gopherjs/js"];
	qunit = $packages["github.com/rusco/qunit"];
	strconv = $packages["strconv"];
	strings = $packages["strings"];
	time = $packages["time"];
	Object = $pkg.Object = $newType(4, $kindMap, "main.Object", "Object", "main", null);
	EvtScenario = $pkg.EvtScenario = $newType(0, $kindStruct, "main.EvtScenario", "EvtScenario", "main", function() {
		this.$val = this;
	});
	working = $pkg.working = $newType(0, $kindStruct, "main.working", "working", "main", function(Deferred_) {
		this.$val = this;
		this.Deferred = Deferred_ !== undefined ? Deferred_ : new jquery.Deferred.ptr();
	});
	sliceType = $sliceType($emptyInterface);
	funcType = $funcType([], [], false);
	sliceType$2 = $sliceType($emptyInterface);
	funcType$1 = $funcType([], [], false);
	sliceType$3 = $sliceType($emptyInterface);
	funcType$2 = $funcType([], [], false);
	funcType$3 = $funcType([], [], false);
	sliceType$4 = $sliceType($emptyInterface);
	arrayType = $arrayType($String, 2);
	sliceType$5 = $sliceType($String);
	mapType = $mapType($String, $emptyInterface);
	ptrType = $ptrType(js.Object);
	funcType$4 = $funcType([], [ptrType], false);
	mapType$1 = $mapType($String, $emptyInterface);
	sliceType$6 = $sliceType($emptyInterface);
	mapType$2 = $mapType($String, $emptyInterface);
	sliceType$7 = $sliceType($emptyInterface);
	funcType$5 = $funcType([], [$emptyInterface], false);
	sliceType$8 = $sliceType($emptyInterface);
	sliceType$9 = $sliceType($emptyInterface);
	funcType$6 = $funcType([$Int, $String], [$String], false);
	sliceType$10 = $sliceType($emptyInterface);
	funcType$7 = $funcType([jquery.Event], [], false);
	sliceType$11 = $sliceType($emptyInterface);
	sliceType$12 = $sliceType($emptyInterface);
	sliceType$13 = $sliceType($emptyInterface);
	funcType$8 = $funcType([], [], false);
	sliceType$14 = $sliceType($String);
	sliceType$15 = $sliceType($String);
	sliceType$16 = $sliceType($emptyInterface);
	sliceType$17 = $sliceType($emptyInterface);
	funcType$9 = $funcType([jquery.Event], [], false);
	sliceType$18 = $sliceType($emptyInterface);
	sliceType$19 = $sliceType($emptyInterface);
	mapType$3 = $mapType($String, $emptyInterface);
	sliceType$20 = $sliceType($emptyInterface);
	sliceType$21 = $sliceType($emptyInterface);
	sliceType$22 = $sliceType($emptyInterface);
	mapType$4 = $mapType($String, $emptyInterface);
	sliceType$23 = $sliceType($emptyInterface);
	funcType$10 = $funcType([jquery.Event], [], false);
	sliceType$24 = $sliceType($emptyInterface);
	mapType$5 = $mapType($String, $emptyInterface);
	funcType$11 = $funcType([jquery.Event], [], false);
	sliceType$25 = $sliceType($emptyInterface);
	funcType$12 = $funcType([jquery.Event], [], false);
	mapType$6 = $mapType($String, $emptyInterface);
	funcType$13 = $funcType([jquery.Event], [], false);
	sliceType$26 = $sliceType($emptyInterface);
	funcType$14 = $funcType([jquery.Event], [], false);
	funcType$15 = $funcType([$Int], [$Bool], false);
	sliceType$27 = $sliceType($emptyInterface);
	funcType$16 = $funcType([], [], false);
	sliceType$28 = $sliceType($emptyInterface);
	sliceType$29 = $sliceType($emptyInterface);
	funcType$17 = $funcType([jquery.Event], [], false);
	funcType$18 = $funcType([], [], false);
	funcType$19 = $funcType([Object], [], false);
	funcType$20 = $funcType([Object], [], false);
	funcType$21 = $funcType([$emptyInterface], [], false);
	funcType$22 = $funcType([], [], false);
	sliceType$30 = $sliceType($emptyInterface);
	funcType$23 = $funcType([$emptyInterface, $String, $emptyInterface], [], false);
	sliceType$31 = $sliceType($emptyInterface);
	funcType$24 = $funcType([$emptyInterface, $String, $emptyInterface], [], false);
	sliceType$32 = $sliceType($emptyInterface);
	funcType$25 = $funcType([$emptyInterface], [], false);
	mapType$7 = $mapType($String, $emptyInterface);
	sliceType$33 = $sliceType($emptyInterface);
	funcType$26 = $funcType([$emptyInterface], [], false);
	sliceType$34 = $sliceType($emptyInterface);
	funcType$27 = $funcType([Object], [], false);
	funcType$28 = $funcType([Object], [], false);
	funcType$29 = $funcType([$emptyInterface], [], false);
	funcType$30 = $funcType([$emptyInterface, $String, $emptyInterface], [], false);
	sliceType$35 = $sliceType($emptyInterface);
	funcType$31 = $funcType([$emptyInterface], [], false);
	mapType$8 = $mapType($String, $emptyInterface);
	funcType$32 = $funcType([$emptyInterface, $String, $emptyInterface], [], false);
	sliceType$36 = $sliceType($emptyInterface);
	funcType$33 = $funcType([$emptyInterface], [], false);
	mapType$9 = $mapType($String, $emptyInterface);
	sliceType$37 = $sliceType($emptyInterface);
	funcType$34 = $funcType([$emptyInterface], [], false);
	funcType$35 = $funcType([$emptyInterface], [], false);
	funcType$36 = $funcType([$emptyInterface], [], false);
	sliceType$38 = $sliceType($emptyInterface);
	funcType$37 = $funcType([], [], false);
	sliceType$39 = $sliceType($emptyInterface);
	funcType$38 = $funcType([$String], [], false);
	funcType$39 = $funcType([$String], [], false);
	funcType$40 = $funcType([], [], false);
	sliceType$40 = $sliceType($emptyInterface);
	funcType$41 = $funcType([], [], false);
	funcType$42 = $funcType([], [], false);
	sliceType$41 = $sliceType($emptyInterface);
	funcType$43 = $funcType([], [], false);
	funcType$44 = $funcType([], [], false);
	funcType$45 = $funcType([], [], false);
	funcType$46 = $funcType([], [], false);
	funcType$47 = $funcType([], [], false);
	funcType$48 = $funcType([], [], false);
	funcType$49 = $funcType([$Int], [$Int], false);
	funcType$50 = $funcType([$Int], [], false);
	funcType$51 = $funcType([$Int], [$Int], false);
	funcType$52 = $funcType([$Int], [], false);
	EvtScenario.ptr.prototype.Setup = function() {
		var s;
		s = $clone(this, EvtScenario);
		jQuery(new sliceType([new $String("<p id=\"firstp\">See \n\t\t\t<a id=\"someid\" href=\"somehref\" rel=\"bookmark\">this blog entry</a>\n\t\t\tfor more information.</p>")])).AppendTo(new $String("#qunit-fixture"));
	};
	EvtScenario.prototype.Setup = function() { return this.$val.Setup(); };
	EvtScenario.ptr.prototype.Teardown = function() {
		var s;
		s = $clone(this, EvtScenario);
		jQuery(new sliceType([new $String("#qunit-fixture")])).Empty();
	};
	EvtScenario.prototype.Teardown = function() { return this.$val.Teardown(); };
	getDocumentBody = function() {
		return $global.document.body;
	};
	stringify = function(i) {
		var i;
		return $internalize($global.JSON.stringify($externalize(i, $emptyInterface)), $String);
	};
	NewWorking = $pkg.NewWorking = function(d) {
		var d;
		d = $clone(d, jquery.Deferred);
		return new working.ptr($clone(d, jquery.Deferred));
	};
	working.ptr.prototype.notify = function() {
		var w;
		w = $clone(this, working);
		if (w.Deferred.State() === "pending") {
			w.Deferred.Notify(new $String("working... "));
			$global.setTimeout($externalize($methodVal(w, "notify"), funcType), 500);
		}
	};
	working.prototype.notify = function() { return this.$val.notify(); };
	working.ptr.prototype.hi = function(name) {
		var name, w;
		w = $clone(this, working);
		if (name === "John") {
			countJohn = countJohn + (1) >> 0;
		} else if (name === "Karl") {
			countKarl = countKarl + (1) >> 0;
		}
	};
	working.prototype.hi = function(name) { return this.$val.hi(name); };
	asyncEvent = function(accept, i) {
		var accept, dfd, i, wx;
		dfd = $clone(jquery.NewDeferred(), jquery.Deferred);
		if (accept) {
			$global.setTimeout($externalize((function() {
				dfd.Resolve(new sliceType$2([new $String("hurray")]));
			}), funcType$1), 200 * i >> 0);
		} else {
			$global.setTimeout($externalize((function() {
				dfd.Reject(new sliceType$3([new $String("sorry")]));
			}), funcType$2), 210 * i >> 0);
		}
		wx = $clone(NewWorking(dfd), working);
		$global.setTimeout($externalize($methodVal(wx, "notify"), funcType$3), 1);
		return dfd.Promise();
	};
	main = function() {
		var x;
		qunit.Module("Core");
		qunit.Test("jQuery Properties", (function(assert) {
			var assert, jQ2;
			assert.Equal(new $String($internalize(jQuery(new sliceType([])).o.jquery, $String)), new $String("2.1.1"), "JQuery Version");
			assert.Equal(new $Int(($parseInt(jQuery(new sliceType([])).o.length) >> 0)), new $Int(0), "jQuery().Length");
			jQ2 = $clone(jQuery(new sliceType([new $String("body")])), jquery.JQuery);
			assert.Equal(new $String($internalize(jQ2.o.selector, $String)), new $String("body"), "jQ2 := jQuery(\"body\"); jQ2.Selector.Selector");
			assert.Equal(new $String($internalize(jQuery(new sliceType([new $String("body")])).o.selector, $String)), new $String("body"), "jQuery(\"body\").Selector");
		}));
		qunit.Test("Test Setup", (function(assert) {
			var assert, test;
			test = $clone(jQuery(new sliceType([new $jsObjectPtr(getDocumentBody())])).Find(new sliceType$4([new $String("#qunit-fixture")])), jquery.JQuery);
			assert.Equal(new $String($internalize(test.o.selector, $String)), new $String("#qunit-fixture"), "#qunit-fixture find Selector");
			assert.Equal(new $String($internalize(test.o.context, $String)), new $jsObjectPtr(getDocumentBody()), "#qunit-fixture find Context");
		}));
		qunit.Test("Static Functions", (function(assert) {
			var _key, _key$1, _map, _map$1, assert, o, x;
			jquery.GlobalEval("var globalEvalTest = 2;");
			assert.Equal(new $Int(($parseInt($global.globalEvalTest) >> 0)), new $Int(2), "GlobalEval: Test variable declarations are global");
			assert.Equal(new $String(jquery.Trim("  GopherJS  ")), new $String("GopherJS"), "Trim: leading and trailing space");
			assert.Equal(new $String(jquery.Type(new $Bool(true))), new $String("boolean"), "Type: Boolean");
			assert.Equal(new $String(jquery.Type((x = time.Now(), new x.constructor.elem(x)))), new $String("date"), "Type: Date");
			assert.Equal(new $String(jquery.Type(new $String("GopherJS"))), new $String("string"), "Type: String");
			assert.Equal(new $String(jquery.Type(new $Float64(12.21))), new $String("number"), "Type: Number");
			assert.Equal(new $String(jquery.Type($ifaceNil)), new $String("null"), "Type: Null");
			assert.Equal(new $String(jquery.Type(new arrayType($toNativeArray($kindString, ["go", "lang"])))), new $String("array"), "Type: Array");
			assert.Equal(new $String(jquery.Type(new sliceType$5(["go", "lang"]))), new $String("array"), "Type: Array");
			o = (_map = new $Map(), _key = "a", _map[_key] = { k: _key, v: new $Bool(true) }, _key = "b", _map[_key] = { k: _key, v: new $Float64(1.1) }, _key = "c", _map[_key] = { k: _key, v: new $String("more") }, _map);
			assert.Equal(new $String(jquery.Type(new mapType(o))), new $String("object"), "Type: Object");
			assert.Equal(new $String(jquery.Type(new funcType$4(getDocumentBody))), new $String("function"), "Type: Function");
			assert.Ok(new $Bool(!jquery.IsPlainObject(new $String(""))), "IsPlainObject: string");
			assert.Ok(new $Bool(jquery.IsPlainObject(new mapType(o))), "IsPlainObject: Object");
			assert.Ok(new $Bool(!jquery.IsEmptyObject(new mapType(o))), "IsEmptyObject: Object");
			assert.Ok(new $Bool(jquery.IsEmptyObject(new mapType$1((_map$1 = new $Map(), _map$1)))), "IsEmptyObject: Object");
			assert.Ok(new $Bool(!jquery.IsFunction(new $String(""))), "IsFunction: string");
			assert.Ok(new $Bool(jquery.IsFunction(new funcType$4(getDocumentBody))), "IsFunction: getDocumentBody");
			assert.Ok(new $Bool(!jquery.IsNumeric(new $String("a3a"))), "IsNumeric: string");
			assert.Ok(new $Bool(jquery.IsNumeric(new $String("0xFFF"))), "IsNumeric: hex");
			assert.Ok(new $Bool(jquery.IsNumeric(new $String("8e-2"))), "IsNumeric: exponential");
			assert.Ok(new $Bool(!jquery.IsXMLDoc(new funcType$4(getDocumentBody))), "HTML Body element");
			assert.Ok(new $Bool(jquery.IsWindow(new $jsObjectPtr($global))), "window");
		}));
		qunit.Test("ToArray,InArray", (function(assert) {
			var _i, _ref, arr, assert, divs, str, v;
			jQuery(new sliceType([new $String("<div>a</div>\n\t\t\t\t<div>b</div>\n\t\t\t\t<div>c</div>")])).AppendTo(new $String("#qunit-fixture"));
			divs = $clone(jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("div")])), jquery.JQuery);
			assert.Equal(new $Int(($parseInt(divs.o.length) >> 0)), new $Int(3), "3 divs in Fixture inserted");
			str = "";
			_ref = divs.ToArray();
			_i = 0;
			while (true) {
				if (!(_i < _ref.$length)) { break; }
				v = ((_i < 0 || _i >= _ref.$length) ? $throwRuntimeError("index out of range") : _ref.$array[_ref.$offset + _i]);
				str = str + (jQuery(new sliceType([v])).Text());
				_i++;
			}
			assert.Equal(new $String(str), new $String("abc"), "ToArray() allows range over selection");
			arr = new sliceType$6([new $String("a"), new $Int(3), new $Bool(true), new $Float64(2.2), new $String("GopherJS")]);
			assert.Equal(new $Int(jquery.InArray(new $Int(4), arr)), new $Int(-1), "InArray");
			assert.Equal(new $Int(jquery.InArray(new $Int(3), arr)), new $Int(1), "InArray");
			assert.Equal(new $Int(jquery.InArray(new $String("a"), arr)), new $Int(0), "InArray");
			assert.Equal(new $Int(jquery.InArray(new $String("b"), arr)), new $Int(-1), "InArray");
			assert.Equal(new $Int(jquery.InArray(new $String("GopherJS"), arr)), new $Int(4), "InArray");
		}));
		qunit.Test("ParseHTML, ParseXML, ParseJSON", (function(assert) {
			var _entry, arr, assert, language, obj, str, xml, xmlDoc;
			str = "<ul>\n  \t\t\t\t<li class=\"firstclass\">list item 1</li>\n  \t\t\t\t<li>list item 2</li>\n  \t\t\t\t<li>list item 3</li>\n  \t\t\t\t<li>list item 4</li>\n  \t\t\t\t<li class=\"lastclass\">list item 5</li>\n\t\t\t\t</ul>";
			arr = jquery.ParseHTML(str);
			jQuery(new sliceType([arr])).AppendTo(new $String("#qunit-fixture"));
			assert.Equal(new $Int(($parseInt(jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("ul li")])).o.length) >> 0)), new $Int(5), "ParseHTML");
			xml = "<rss version='2.0'><channel><title>RSS Title</title></channel></rss>";
			xmlDoc = jquery.ParseXML(xml);
			assert.Equal(new $String(jQuery(new sliceType([xmlDoc])).Find(new sliceType$4([new $String("title")])).Text()), new $String("RSS Title"), "ParseXML");
			obj = jquery.ParseJSON("{ \"language\": \"go\" }");
			language = $assertType((_entry = $assertType(obj, mapType$2)["language"], _entry !== undefined ? _entry.v : $ifaceNil), $String);
			assert.Equal(new $String(language), new $String("go"), "ParseJSON");
		}));
		qunit.Test("Grep", (function(assert) {
			var arr, arr2, assert;
			arr = new sliceType$7([new $Int(1), new $Int(9), new $Int(3), new $Int(8), new $Int(6), new $Int(1), new $Int(5), new $Int(9), new $Int(4), new $Int(7), new $Int(3), new $Int(8), new $Int(6), new $Int(9), new $Int(1)]);
			arr2 = jquery.Grep(arr, (function(n, idx) {
				var idx, n;
				return !(($assertType(n, $Float64) === 5)) && idx > 4;
			}));
			assert.Equal(new $Int(arr2.$length), new $Int(9), "Grep");
		}));
		qunit.Test("Noop,Now", (function(assert) {
			var assert, callSth, date, time$1;
			callSth = (function(fn) {
				var fn;
				return fn();
			});
			callSth(jquery.Noop);
			jquery.Noop();
			assert.Ok(new $Bool(jquery.IsFunction(new funcType$5(jquery.Noop))), "jquery.Noop");
			date = new ($global.Date)();
			time$1 = $parseFloat(date.getTime());
			assert.Ok(new $Bool(time$1 <= jquery.Now()), "jquery.Now()");
		}));
		qunit.Module("Dom");
		qunit.Test("AddClass,Clone,Add,AppendTo,Find", (function(assert) {
			var assert, html, txt;
			jQuery(new sliceType([new $String("p")])).AddClass(new $String("wow")).Clone(new sliceType$8([])).Add(new sliceType$9([new $String("<span id='dom02'>WhatADay</span>")])).AppendTo(new $String("#qunit-fixture"));
			txt = jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("span#dom02")])).Text();
			assert.Equal(new $String(txt), new $String("WhatADay"), "Test of Clone, Add, AppendTo, Find, Text Functions");
			jQuery(new sliceType([new $String("#qunit-fixture")])).Empty();
			html = "\n\t\t\t<div>This div should be white</div>\n\t\t\t<div class=\"red\">This div will be green because it now has the \"green\" and \"red\" classes.\n\t\t\t   It would be red if the addClass function failed.</div>\n\t\t\t<div>This div should be white</div>\n\t\t\t<p>There are zero green divs</p>\n\n\t\t\t<button>some btn</button>";
			jQuery(new sliceType([new $String(html)])).AppendTo(new $String("#qunit-fixture"));
			jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("div")])).AddClass(new funcType$6((function(index, currentClass) {
				var addedClass, currentClass, index;
				addedClass = "";
				if (currentClass === "red") {
					addedClass = "green";
					jQuery(new sliceType([new $String("p")])).SetText(new $String("There is one green div"));
				}
				return addedClass;
			})));
			jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("button")])).AddClass(new $String("red"));
			assert.Ok(new $Bool(jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("button")])).HasClass("red")), "button hasClass red");
			assert.Ok(new $Bool(jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("p")])).Text() === "There is one green div"), "There is one green div");
			assert.Ok(new $Bool(jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("div:eq(1)")])).HasClass("green")), "one div hasClass green");
			jQuery(new sliceType([new $String("#qunit-fixture")])).Empty();
		}));
		qunit.Test("Children,Append", (function(assert) {
			var assert, j, x, x$1;
			j = $clone(jQuery(new sliceType([new $String("<div class=\"pipe animated\"><div class=\"pipe_upper\" style=\"height: 79px;\"></div><div class=\"guess top\" style=\"top: 114px;\"></div><div class=\"pipe_middle\" style=\"height: 100px; top: 179px;\"></div><div class=\"guess bottom\" style=\"bottom: 76px;\"></div><div class=\"pipe_lower\" style=\"height: 41px;\"></div><div class=\"question\"></div></div>")])), jquery.JQuery);
			assert.Ok(new $Bool((j.Html().length === 301)), "jQuery html len");
			j.Children(new $String(".question")).Append(new sliceType$10([(x = jQuery(new sliceType([new $String("<div class = \"question_digit first\" style = \"background-image: url('assets/font_big_3.png');\"></div>")])), new x.constructor.elem(x))]));
			assert.Ok(new $Bool((j.Html().length === 397)), "jquery html len after 1st jquery object append");
			j.Children(new $String(".question")).Append(new sliceType$10([(x$1 = jQuery(new sliceType([new $String("<div class = \"question_digit symbol\" style=\"background-image: url('assets/font_shitty_x.png');\"></div>")])), new x$1.constructor.elem(x$1))]));
			assert.Ok(new $Bool((j.Html().length === 497)), "jquery htm len after 2nd jquery object append");
			j.Children(new $String(".question")).Append(new sliceType$10([new $String("<div class = \"question_digit second\" style = \"background-image: url('assets/font_big_1.png');\"></div>")]));
			assert.Ok(new $Bool((j.Html().length === 594)), "jquery html len after html append");
		}));
		qunit.Test("ApiOnly:ScollFn,SetCss,CssArray,FadeOut", (function(assert) {
			var assert, htmlsnippet, i;
			i = 0;
			while (true) {
				if (!(i < 3)) { break; }
				jQuery(new sliceType([new $String("p")])).Clone(new sliceType$8([])).AppendTo(new $String("#qunit-fixture"));
				i = i + (1) >> 0;
			}
			jQuery(new sliceType([new $String("#qunit-fixture")])).Scroll(new sliceType$13([new funcType$7((function(e) {
				var e;
				jQuery(new sliceType([new $String("span")])).SetCss(new sliceType$11([new $String("display"), new $String("inline")])).FadeOut(new sliceType$12([new $String("slow")]));
			}))]));
			htmlsnippet = "<style>\n\t\t\t  div {\n\t\t\t    height: 50px;\n\t\t\t    margin: 5px;\n\t\t\t    padding: 5px;\n\t\t\t    float: left;\n\t\t\t  }\n\t\t\t  #box1 {\n\t\t\t    width: 50px;\n\t\t\t    color: yellow;\n\t\t\t    background-color: blue;\n\t\t\t  }\n\t\t\t  #box2 {\n\t\t\t    width: 80px;\n\t\t\t    color: rgb(255, 255, 255);\n\t\t\t    background-color: rgb(15, 99, 30);\n\t\t\t  }\n\t\t\t  #box3 {\n\t\t\t    width: 40px;\n\t\t\t    color: #fcc;\n\t\t\t    background-color: #123456;\n\t\t\t  }\n\t\t\t  #box4 {\n\t\t\t    width: 70px;\n\t\t\t    background-color: #f11;\n\t\t\t  }\n\t\t\t  </style>\n\t\t\t \n\t\t\t<p id=\"result\">&nbsp;</p>\n\t\t\t<div id=\"box1\">1</div>\n\t\t\t<div id=\"box2\">2</div>\n\t\t\t<div id=\"box3\">3</div>\n\t\t\t<div id=\"box4\">4</div>";
			jQuery(new sliceType([new $String(htmlsnippet)])).AppendTo(new $String("#qunit-fixture"));
			jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("div")])).On(new sliceType$16([new $String("click"), new funcType$8((function() {
				var _entry, _i, _keys, _ref, html, prop, styleProps, value;
				html = new sliceType$14(["The clicked div has the following styles:"]);
				styleProps = jQuery(new sliceType([new $jsObjectPtr(this)])).CssArray(new sliceType$15(["width", "height"]));
				_ref = styleProps;
				_i = 0;
				_keys = $keys(_ref);
				while (true) {
					if (!(_i < _keys.length)) { break; }
					_entry = _ref[_keys[_i]];
					if (_entry === undefined) {
						_i++;
						continue;
					}
					prop = _entry.k;
					value = _entry.v;
					html = $append(html, prop + ": " + $assertType(value, $String));
					_i++;
				}
				jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("#result")])).SetHtml(new $String(strings.Join(html, "<br>")));
			}))]));
			jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("div:eq(0)")])).Trigger(new sliceType$17([new $String("click")]));
			assert.Ok(new $Bool(jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("#result")])).Html() === "The clicked div has the following styles:<br>width: 50px<br>height: 50px"), "CssArray read properties");
		}));
		qunit.Test("ApiOnly:SelectFn,SetText,Show,FadeOut", (function(assert) {
			var assert;
			qunit.Expect(0);
			jQuery(new sliceType([new $String("<p>Click and drag the mouse to select text in the inputs.</p>\n  \t\t\t\t<input type=\"text\" value=\"Some text\">\n  \t\t\t\t<input type=\"text\" value=\"to test on\">\n  \t\t\t\t<div></div>")])).AppendTo(new $String("#qunit-fixture"));
			jQuery(new sliceType([new $String(":input")])).Select(new sliceType$18([new funcType$9((function(e) {
				var e;
				jQuery(new sliceType([new $String("div")])).SetText(new $String("Something was selected")).Show().FadeOut(new sliceType$12([new $String("1000")]));
			}))]));
		}));
		qunit.Test("Eq,Find", (function(assert) {
			var assert;
			jQuery(new sliceType([new $String("<div></div>\n\t\t\t\t<div></div>\n\t\t\t\t<div class=\"test\"></div>\n\t\t\t\t<div></div>\n\t\t\t\t<div></div>\n\t\t\t\t<div></div>")])).AppendTo(new $String("#qunit-fixture"));
			assert.Ok(new $Bool(jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("div")])).Eq(2).HasClass("test")), "Eq(2) has class test");
			assert.Ok(new $Bool(!jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("div")])).Eq(0).HasClass("test")), "Eq(0) has no class test");
		}));
		qunit.Test("Find,End", (function(assert) {
			var assert;
			jQuery(new sliceType([new $String("<p class='ok'><span class='notok'>Hello</span>, how are you?</p>")])).AppendTo(new $String("#qunit-fixture"));
			assert.Ok(new $Bool(jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("p")])).Find(new sliceType$4([new $String("span")])).HasClass("notok")), "before call to end");
			assert.Ok(new $Bool(jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("p")])).Find(new sliceType$4([new $String("span")])).End().HasClass("ok")), "after call to end");
		}));
		qunit.Test("Slice,Attr,First,Last", (function(assert) {
			var assert;
			jQuery(new sliceType([new $String("<ul>\n  \t\t\t\t<li class=\"firstclass\">list item 1</li>\n  \t\t\t\t<li>list item 2</li>\n  \t\t\t\t<li>list item 3</li>\n  \t\t\t\t<li>list item 4</li>\n  \t\t\t\t<li class=\"lastclass\">list item 5</li>\n\t\t\t\t</ul>")])).AppendTo(new $String("#qunit-fixture"));
			assert.Equal(new $Int(($parseInt(jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("li")])).Slice(new sliceType$19([new $Int(2)])).o.length) >> 0)), new $Int(3), "Slice");
			assert.Equal(new $Int(($parseInt(jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("li")])).Slice(new sliceType$19([new $Int(2), new $Int(4)])).o.length) >> 0)), new $Int(2), "SliceByEnd");
			assert.Equal(new $String(jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("li")])).First().Attr("class")), new $String("firstclass"), "First");
			assert.Equal(new $String(jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("li")])).Last().Attr("class")), new $String("lastclass"), "Last");
		}));
		qunit.Test("Css", (function(assert) {
			var _key, _map, assert, div, span;
			jQuery(new sliceType([new $String("#qunit-fixture")])).SetCss(new sliceType$11([new mapType$3((_map = new $Map(), _key = "color", _map[_key] = { k: _key, v: new $String("red") }, _key = "background", _map[_key] = { k: _key, v: new $String("blue") }, _key = "width", _map[_key] = { k: _key, v: new $String("20px") }, _key = "height", _map[_key] = { k: _key, v: new $String("10px") }, _map))]));
			assert.Ok(new $Bool(jQuery(new sliceType([new $String("#qunit-fixture")])).Css("width") === "20px" && jQuery(new sliceType([new $String("#qunit-fixture")])).Css("height") === "10px"), "SetCssMap");
			div = $clone(jQuery(new sliceType([new $String("<div style='display: inline'/>")])).Show().AppendTo(new $String("#qunit-fixture")), jquery.JQuery);
			assert.Equal(new $String(div.Css("display")), new $String("inline"), "Make sure that element has same display when it was created.");
			div.Remove(new sliceType$20([]));
			span = $clone(jQuery(new sliceType([new $String("<span/>")])).Hide().Show(), jquery.JQuery);
			assert.Equal(new $jsObjectPtr(span.Get(new sliceType$21([new $Int(0)])).style.display), new $String("inline"), "For detached span elements, display should always be inline");
			span.Remove(new sliceType$20([]));
		}));
		qunit.Test("Attributes", (function(assert) {
			var _key, _map, assert, extras, input;
			jQuery(new sliceType([new $String("<form id='testForm'></form>")])).AppendTo(new $String("#qunit-fixture"));
			extras = $clone(jQuery(new sliceType([new $String("<input id='id' name='id' /><input id='name' name='name' /><input id='target' name='target' />")])).AppendTo(new $String("#testForm")), jquery.JQuery);
			assert.Equal(new $String(jQuery(new sliceType([new $String("#testForm")])).Attr("target")), new $String(""), "Attr");
			assert.Equal(new $String(jQuery(new sliceType([new $String("#testForm")])).SetAttr(new sliceType$22([new $String("target"), new $String("newTarget")])).Attr("target")), new $String("newTarget"), "SetAttr2");
			assert.Equal(new $String(jQuery(new sliceType([new $String("#testForm")])).RemoveAttr("id").Attr("id")), new $String(""), "RemoveAttr ");
			assert.Equal(new $String(jQuery(new sliceType([new $String("#testForm")])).Attr("name")), new $String(""), "Attr undefined");
			extras.Remove(new sliceType$20([]));
			jQuery(new sliceType([new $String("<a/>")])).SetAttr(new sliceType$22([new mapType$4((_map = new $Map(), _key = "id", _map[_key] = { k: _key, v: new $String("tAnchor5") }, _key = "href", _map[_key] = { k: _key, v: new $String("#5") }, _map))])).AppendTo(new $String("#qunit-fixture"));
			assert.Equal(new $String(jQuery(new sliceType([new $String("#tAnchor5")])).Attr("href")), new $String("#5"), "Attr");
			jQuery(new sliceType([new $String("<a id='tAnchor6' href='#5' />")])).AppendTo(new $String("#qunit-fixture"));
			assert.Equal(jQuery(new sliceType([new $String("#tAnchor5")])).Prop("href"), jQuery(new sliceType([new $String("#tAnchor6")])).Prop("href"), "Prop");
			input = $clone(jQuery(new sliceType([new $String("<input name='tester' />")])), jquery.JQuery);
			assert.StrictEqual(new $jsObjectPtr(input.Clone(new sliceType$8([new $Bool(true)])).SetAttr(new sliceType$22([new $String("name"), new $String("test")])).Underlying()[0].name), new $String("test"), "Clone");
			jQuery(new sliceType([new $String("#qunit-fixture")])).Empty();
			jQuery(new sliceType([new $String("<input type=\"checkbox\" checked=\"checked\">\n  \t\t\t<input type=\"checkbox\">\n  \t\t\t<input type=\"checkbox\">\n  \t\t\t<input type=\"checkbox\" checked=\"checked\">")])).AppendTo(new $String("#qunit-fixture"));
			jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("input[type='checkbox']")])).SetProp(new sliceType$23([new $String("disabled"), new $Bool(true)]));
			assert.Ok(jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("input[type='checkbox']")])).Prop("disabled"), "SetProp");
		}));
		qunit.Test("Unique", (function(assert) {
			var assert, divs, divs2, divs3;
			jQuery(new sliceType([new $String("<div>There are 6 divs in this document.</div>\n\t\t\t\t<div></div>\n\t\t\t\t<div class=\"dup\"></div>\n\t\t\t\t<div class=\"dup\"></div>\n\t\t\t\t<div class=\"dup\"></div>\n\t\t\t\t<div></div>")])).AppendTo(new $String("#qunit-fixture"));
			divs = jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("div")])).Get(new sliceType$21([]));
			assert.Equal(new $jsObjectPtr(divs.length), new $Int(6), "6 divs inserted");
			jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String(".dup")])).Clone(new sliceType$8([new $Bool(true)])).AppendTo(new $String("#qunit-fixture"));
			divs2 = jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("div")])).Get(new sliceType$21([]));
			assert.Equal(new $jsObjectPtr(divs2.length), new $Int(9), "9 divs inserted");
			divs3 = jquery.Unique(divs);
			assert.Equal(new $jsObjectPtr(divs3.length), new $Int(6), "post-qunique should be 6 elements");
		}));
		qunit.Test("Serialize,SerializeArray,Trigger,Submit", (function(assert) {
			var assert, collectResults, serializedString;
			qunit.Expect(2);
			jQuery(new sliceType([new $String("<form>\n\t\t\t\t  <div><input type=\"text\" name=\"a\" value=\"1\" id=\"a\"></div>\n\t\t\t\t  <div><input type=\"text\" name=\"b\" value=\"2\" id=\"b\"></div>\n\t\t\t\t  <div><input type=\"hidden\" name=\"c\" value=\"3\" id=\"c\"></div>\n\t\t\t\t  <div>\n\t\t\t\t    <textarea name=\"d\" rows=\"8\" cols=\"40\">4</textarea>\n\t\t\t\t  </div>\n\t\t\t\t  <div><select name=\"e\">\n\t\t\t\t    <option value=\"5\" selected=\"selected\">5</option>\n\t\t\t\t    <option value=\"6\">6</option>\n\t\t\t\t    <option value=\"7\">7</option>\n\t\t\t\t  </select></div>\n\t\t\t\t  <div>\n\t\t\t\t    <input type=\"checkbox\" name=\"f\" value=\"8\" id=\"f\">\n\t\t\t\t  </div>\n\t\t\t\t  <div>\n\t\t\t\t    <input type=\"submit\" name=\"g\" value=\"Submit\" id=\"g\">\n\t\t\t\t  </div>\n\t\t\t\t</form>")])).AppendTo(new $String("#qunit-fixture"));
			collectResults = "";
			jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("form")])).Submit(new sliceType$24([new funcType$10((function(evt) {
				var evt, i, sa;
				sa = jQuery(new sliceType([new $jsObjectPtr(evt.Object.target)])).SerializeArray();
				i = 0;
				while (true) {
					if (!(i < $parseInt(sa.length))) { break; }
					collectResults = collectResults + ($internalize(sa[i].name, $String));
					i = i + (1) >> 0;
				}
				assert.Equal(new $String(collectResults), new $String("abcde"), "SerializeArray");
				evt.PreventDefault();
			}))]));
			serializedString = "a=1&b=2&c=3&d=4&e=5";
			assert.Equal(new $String(jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("form")])).Serialize()), new $String(serializedString), "Serialize");
			jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("form")])).Trigger(new sliceType$17([new $String("submit")]));
		}));
		qunit.ModuleLifecycle("Events", (x = new EvtScenario.ptr(), new x.constructor.elem(x)));
		qunit.Test("On,One,Off,Trigger", (function(assert) {
			var _key, _key$1, _map, _map$1, _tmp, _tmp$1, assert, clickCounter, data, data2, elem, fn, handler, handlerWithData, mouseoverCounter;
			fn = (function(ev) {
				var ev;
				assert.Ok(new $Bool(!(ev.Object.data === undefined)), "on() with data, check passed data exists");
				assert.Equal(new $jsObjectPtr(ev.Object.data.foo), new $String("bar"), "on() with data, Check value of passed data");
			});
			data = (_map = new $Map(), _key = "foo", _map[_key] = { k: _key, v: new $String("bar") }, _map);
			jQuery(new sliceType([new $String("#firstp")])).On(new sliceType$16([new $String("click"), new mapType$5(data), new funcType$11(fn)])).Trigger(new sliceType$17([new $String("click")])).Off(new sliceType$25([new $String("click"), new funcType$11(fn)]));
			_tmp = 0; _tmp$1 = 0; clickCounter = _tmp; mouseoverCounter = _tmp$1;
			handler = (function(ev) {
				var ev;
				if ($internalize(ev.Object.type, $String) === "click") {
					clickCounter = clickCounter + (1) >> 0;
				} else if ($internalize(ev.Object.type, $String) === "mouseover") {
					mouseoverCounter = mouseoverCounter + (1) >> 0;
				}
			});
			handlerWithData = (function(ev) {
				var ev;
				if ($internalize(ev.Object.type, $String) === "click") {
					clickCounter = clickCounter + (($parseInt(ev.Object.data.data) >> 0)) >> 0;
				} else if ($internalize(ev.Object.type, $String) === "mouseover") {
					mouseoverCounter = mouseoverCounter + (($parseInt(ev.Object.data.data) >> 0)) >> 0;
				}
			});
			data2 = (_map$1 = new $Map(), _key$1 = "data", _map$1[_key$1] = { k: _key$1, v: new $Int(2) }, _map$1);
			elem = $clone(jQuery(new sliceType([new $String("#firstp")])).On(new sliceType$16([new $String("click"), new funcType$12(handler)])).On(new sliceType$16([new $String("mouseover"), new funcType$12(handler)])).One(new sliceType$26([new $String("click"), new mapType$6(data2), new funcType$13(handlerWithData)])).One(new sliceType$26([new $String("mouseover"), new mapType$6(data2), new funcType$13(handlerWithData)])), jquery.JQuery);
			assert.Equal(new $Int(clickCounter), new $Int(0), "clickCounter initialization ok");
			assert.Equal(new $Int(mouseoverCounter), new $Int(0), "mouseoverCounter initialization ok");
			elem.Trigger(new sliceType$17([new $String("click")])).Trigger(new sliceType$17([new $String("mouseover")]));
			assert.Equal(new $Int(clickCounter), new $Int(3), "clickCounter Increased after Trigger/On/One");
			assert.Equal(new $Int(mouseoverCounter), new $Int(3), "mouseoverCounter Increased after Trigger/On/One");
			elem.Trigger(new sliceType$17([new $String("click")])).Trigger(new sliceType$17([new $String("mouseover")]));
			assert.Equal(new $Int(clickCounter), new $Int(4), "clickCounter Increased after Trigger/On");
			assert.Equal(new $Int(mouseoverCounter), new $Int(4), "a) mouseoverCounter Increased after TriggerOn");
			elem.Trigger(new sliceType$17([new $String("click")])).Trigger(new sliceType$17([new $String("mouseover")]));
			assert.Equal(new $Int(clickCounter), new $Int(5), "b) clickCounter not Increased after Off");
			assert.Equal(new $Int(mouseoverCounter), new $Int(5), "c) mouseoverCounter not Increased after Off");
			elem.Off(new sliceType$25([new $String("click")])).Off(new sliceType$25([new $String("mouseover")]));
			elem.Trigger(new sliceType$17([new $String("click")])).Trigger(new sliceType$17([new $String("mouseover")]));
			assert.Equal(new $Int(clickCounter), new $Int(5), "clickCounter not Increased after Off");
			assert.Equal(new $Int(mouseoverCounter), new $Int(5), "mouseoverCounter not Increased after Off");
		}));
		qunit.Test("Each", (function(assert) {
			var assert, blueCount, html, i;
			jQuery(new sliceType([new $String("#qunit-fixture")])).Empty();
			html = "<style>\n\t\t\t  \t\tdiv {\n\t\t\t    \t\tcolor: red;\n\t\t\t    \t\ttext-align: center;\n\t\t\t    \t\tcursor: pointer;\n\t\t\t    \t\tfont-weight: bolder;\n\t\t\t\t    width: 300px;\n\t\t\t\t  }\n\t\t\t\t </style>\n\t\t\t\t <div>Click here</div>\n\t\t\t\t <div>to iterate through</div>\n\t\t\t\t <div>these divs.</div>";
			jQuery(new sliceType([new $String(html)])).AppendTo(new $String("#qunit-fixture"));
			blueCount = 0;
			jQuery(new sliceType([new $String("#qunit-fixture")])).On(new sliceType$16([new $String("click"), new funcType$14((function(e) {
				var e;
				jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("div")])).Each((function(i, elem) {
					var elem, i, style;
					style = jQuery(new sliceType([elem])).Get(new sliceType$21([new $Int(0)])).style;
					if (!($internalize(style.color, $String) === "blue")) {
						style.color = $externalize("blue", $String);
					} else {
						blueCount = blueCount + (1) >> 0;
						style.color = $externalize("", $String);
					}
				}));
			}))]));
			i = 0;
			while (true) {
				if (!(i < 6)) { break; }
				jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("div:eq(0)")])).Trigger(new sliceType$17([new $String("click")]));
				i = i + (1) >> 0;
			}
			assert.Equal(new $Int(($parseInt(jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("div")])).o.length) >> 0)), new $Int(3), "Test setup problem: 3 divs expected");
			assert.Equal(new $Int(blueCount), new $Int(9), "blueCount Counter should be 9");
		}));
		qunit.Test("Filter, Resize", (function(assert) {
			var assert, countFontweight, html;
			jQuery(new sliceType([new $String("#qunit-fixture")])).Empty();
			html = "<style>\n\t\t\t\t  \tdiv {\n\t\t\t\t    \twidth: 60px;\n\t\t\t\t    \theight: 60px;\n\t\t\t\t    \tmargin: 5px;\n\t\t\t\t    \tfloat: left;\n\t\t\t\t    \tborder: 2px white solid;\n\t\t\t\t  \t}\n\t\t\t\t </style>\n\t\t\t\t  \n\t\t\t\t <div></div>\n\t\t\t\t <div class=\"middle\"></div>\n\t\t\t\t <div class=\"middle\"></div>\n\t\t\t\t <div class=\"middle\"></div>\n\t\t\t\t <div class=\"middle\"></div>\n\t\t\t\t <div></div>";
			jQuery(new sliceType([new $String(html)])).AppendTo(new $String("#qunit-fixture"));
			jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("div")])).SetCss(new sliceType$11([new $String("background"), new $String("silver")])).Filter(new sliceType$27([new funcType$15((function(index) {
				var _r, index;
				return (_r = index % 3, _r === _r ? _r : $throwRuntimeError("integer divide by zero")) === 2;
			}))])).SetCss(new sliceType$11([new $String("font-weight"), new $String("bold")]));
			countFontweight = 0;
			jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("div")])).Each((function(i, elem) {
				var elem, fw, i;
				fw = jQuery(new sliceType([elem])).Css("font-weight");
				if (fw === "bold" || fw === "700") {
					countFontweight = countFontweight + (1) >> 0;
				}
			}));
			assert.Equal(new $Int(countFontweight), new $Int(2), "2 divs should have font-weight = 'bold'");
			jQuery(new sliceType([new $jsObjectPtr($global)])).Resize(new sliceType$28([new funcType$16((function() {
				jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("div:eq(0)")])).SetText(new $String(strconv.Itoa(jQuery(new sliceType([new $String("div:eq(0)")])).Width())));
			}))])).Resize(new sliceType$28([]));
			assert.Equal(new $String(jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("div:eq(0)")])).Text()), new $String("60"), "text of first div should be 60");
		}));
		qunit.Test("Not,Offset", (function(assert) {
			var assert, html;
			qunit.Expect(0);
			jQuery(new sliceType([new $String("#qunit-fixture")])).Empty();
			html = "<div></div>\n\t\t\t\t <div id=\"blueone\"></div>\n\t\t\t\t <div></div>\n\t\t\t\t <div class=\"green\"></div>\n\t\t\t\t <div class=\"green\"></div>\n\t\t\t\t <div class=\"gray\"></div>\n\t\t\t\t <div></div>";
			jQuery(new sliceType([new $String(html)])).AppendTo(new $String("#qunit-fixture"));
			jQuery(new sliceType([new $String("#qunit-fixture")])).Find(new sliceType$4([new $String("div")])).Not(new sliceType$29([new $String(".green,#blueone")])).SetCss(new sliceType$11([new $String("border-color"), new $String("red")]));
			jQuery(new sliceType([new $String("*"), new $String("body")])).On(new sliceType$16([new $String("click"), new funcType$17((function(event) {
				var event, offset, tag;
				offset = $clone(jQuery(new sliceType([new $jsObjectPtr(event.Object.target)])).Offset(), jquery.JQueryCoordinates);
				event.StopPropagation();
				tag = $assertType(jQuery(new sliceType([new $jsObjectPtr(event.Object.target)])).Prop("tagName"), $String);
				jQuery(new sliceType([new $String("#result")])).SetText(new $String(tag + " coords ( " + strconv.Itoa(offset.Left) + ", " + strconv.Itoa(offset.Top) + " )"));
			}))]));
		}));
		qunit.Module("Ajax");
		qunit.AsyncTest("Async Dummy Test", (function() {
			qunit.Expect(1);
			return new $jsObjectPtr($global.setTimeout($externalize((function() {
				qunit.Ok(new $Bool(true), " async ok");
				qunit.Start();
			}), funcType$18), 1000));
		}));
		qunit.AsyncTest("Ajax Call", (function() {
			var _key, _map, ajaxopt;
			qunit.Expect(1);
			ajaxopt = (_map = new $Map(), _key = "async", _map[_key] = { k: _key, v: new $Bool(true) }, _key = "type", _map[_key] = { k: _key, v: new $String("POST") }, _key = "url", _map[_key] = { k: _key, v: new $String("http://localhost:3000/nestedjson/") }, _key = "contentType", _map[_key] = { k: _key, v: new $String("application/json charset=utf-8") }, _key = "dataType", _map[_key] = { k: _key, v: new $String("json") }, _key = "data", _map[_key] = { k: _key, v: $ifaceNil }, _key = "beforeSend", _map[_key] = { k: _key, v: new funcType$19((function(data) {
				var data;
			})) }, _key = "success", _map[_key] = { k: _key, v: new funcType$20((function(data) {
				var data, dataStr, expected;
				dataStr = stringify(new Object(data));
				expected = "{\"message\":\"Welcome!\",\"nested\":{\"level\":1,\"moresuccess\":true},\"success\":true}";
				qunit.Ok(new $Bool(dataStr === expected), "Ajax call did not returns expected result");
				qunit.Start();
			})) }, _key = "error", _map[_key] = { k: _key, v: new funcType$21((function(status) {
				var status;
			})) }, _map);
			jquery.Ajax(ajaxopt);
			return $ifaceNil;
		}));
		qunit.AsyncTest("Load", (function() {
			qunit.Expect(1);
			jQuery(new sliceType([new $String("#qunit-fixture")])).Load(new sliceType$30([new $String("/resources/load.html"), new funcType$22((function() {
				qunit.Ok(new $Bool(jQuery(new sliceType([new $String("#qunit-fixture")])).Html() === "<div>load successful!</div>"), "Load call did not returns expected result");
				qunit.Start();
			}))]));
			return $ifaceNil;
		}));
		qunit.AsyncTest("Get", (function() {
			qunit.Expect(1);
			jquery.Get(new sliceType$31([new $String("/resources/get.html"), new funcType$23((function(data, status, xhr) {
				var data, status, xhr;
				qunit.Ok(new $Bool($interfaceIsEqual(data, new $String("<div>get successful!</div>"))), "Get call did not returns expected result");
				qunit.Start();
			}))]));
			return $ifaceNil;
		}));
		qunit.AsyncTest("Post", (function() {
			qunit.Expect(1);
			jquery.Post(new sliceType$32([new $String("/gopher"), new funcType$24((function(data, status, xhr) {
				var data, status, xhr;
				qunit.Ok(new $Bool($interfaceIsEqual(data, new $String("<div>Welcome gopher</div>"))), "Post call did not returns expected result");
				qunit.Start();
			}))]));
			return $ifaceNil;
		}));
		qunit.AsyncTest("GetJSON", (function() {
			qunit.Expect(1);
			jquery.GetJSON(new sliceType$33([new $String("/json/1"), new funcType$25((function(data) {
				var _entry, _tuple, data, ok, val;
				_tuple = (_entry = $assertType(data, mapType$7)["json"], _entry !== undefined ? [_entry.v, true] : [$ifaceNil, false]); val = _tuple[0]; ok = _tuple[1];
				if (ok) {
					qunit.Ok(new $Bool($interfaceIsEqual(val, new $String("1"))), "Json call did not returns expected result");
					qunit.Start();
				}
			}))]));
			return $ifaceNil;
		}));
		qunit.AsyncTest("GetScript", (function() {
			qunit.Expect(1);
			jquery.GetScript(new sliceType$34([new $String("/script"), new funcType$26((function(data) {
				var data;
				qunit.Ok(new $Bool(($assertType(data, $String).length === 29)), "GetScript call did not returns expected result");
				qunit.Start();
			}))]));
			return $ifaceNil;
		}));
		qunit.AsyncTest("AjaxSetup", (function() {
			var _key, _key$1, _map, _map$1, ajaxSetupOptions, ajaxopt;
			qunit.Expect(1);
			ajaxSetupOptions = (_map = new $Map(), _key = "async", _map[_key] = { k: _key, v: new $Bool(true) }, _key = "type", _map[_key] = { k: _key, v: new $String("POST") }, _key = "url", _map[_key] = { k: _key, v: new $String("/nestedjson/") }, _key = "contentType", _map[_key] = { k: _key, v: new $String("application/json charset=utf-8") }, _map);
			jquery.AjaxSetup(ajaxSetupOptions);
			ajaxopt = (_map$1 = new $Map(), _key$1 = "dataType", _map$1[_key$1] = { k: _key$1, v: new $String("json") }, _key$1 = "data", _map$1[_key$1] = { k: _key$1, v: $ifaceNil }, _key$1 = "beforeSend", _map$1[_key$1] = { k: _key$1, v: new funcType$27((function(data) {
				var data;
			})) }, _key$1 = "success", _map$1[_key$1] = { k: _key$1, v: new funcType$28((function(data) {
				var data, dataStr, expected;
				dataStr = stringify(new Object(data));
				expected = "{\"message\":\"Welcome!\",\"nested\":{\"level\":1,\"moresuccess\":true},\"success\":true}";
				qunit.Ok(new $Bool(dataStr === expected), "AjaxSetup call did not returns expected result");
				qunit.Start();
			})) }, _key$1 = "error", _map$1[_key$1] = { k: _key$1, v: new funcType$29((function(status) {
				var status;
			})) }, _map$1);
			jquery.Ajax(ajaxopt);
			return $ifaceNil;
		}));
		qunit.AsyncTest("AjaxPrefilter", (function() {
			qunit.Expect(1);
			jquery.AjaxPrefilter(new sliceType$35([new $String("+json"), new funcType$30((function(options, originalOptions, jqXHR) {
				var jqXHR, options, originalOptions;
			}))]));
			jquery.GetJSON(new sliceType$33([new $String("/json/3"), new funcType$31((function(data) {
				var _entry, _tuple, data, ok, val;
				_tuple = (_entry = $assertType(data, mapType$8)["json"], _entry !== undefined ? [_entry.v, true] : [$ifaceNil, false]); val = _tuple[0]; ok = _tuple[1];
				if (ok) {
					qunit.Ok(new $Bool($assertType(val, $String) === "3"), "AjaxPrefilter call did not returns expected result");
					qunit.Start();
				}
			}))]));
			return $ifaceNil;
		}));
		qunit.AsyncTest("AjaxTransport", (function() {
			qunit.Expect(1);
			jquery.AjaxTransport(new sliceType$36([new $String("+json"), new funcType$32((function(options, originalOptions, jqXHR) {
				var jqXHR, options, originalOptions;
			}))]));
			jquery.GetJSON(new sliceType$33([new $String("/json/4"), new funcType$33((function(data) {
				var _entry, _tuple, data, ok, val;
				_tuple = (_entry = $assertType(data, mapType$9)["json"], _entry !== undefined ? [_entry.v, true] : [$ifaceNil, false]); val = _tuple[0]; ok = _tuple[1];
				if (ok) {
					qunit.Ok(new $Bool($assertType(val, $String) === "4"), "AjaxTransport call did not returns expected result");
					qunit.Start();
				}
			}))]));
			return $ifaceNil;
		}));
		qunit.Module("Deferreds");
		qunit.AsyncTest("Deferreds Test 01", (function() {
			var _r, _tmp, _tmp$1, _tmp$2, fail, i, pass, progress;
			qunit.Expect(1);
			_tmp = 0; _tmp$1 = 0; _tmp$2 = 0; pass = _tmp; fail = _tmp$1; progress = _tmp$2;
			i = 0;
			while (true) {
				if (!(i < 10)) { break; }
				jquery.When(new sliceType$37([new $jsObjectPtr(asyncEvent((_r = i % 2, _r === _r ? _r : $throwRuntimeError("integer divide by zero")) === 0, i))])).Then(new sliceType$38([new funcType$34((function(status) {
					var status;
					pass = pass + (1) >> 0;
				})), new funcType$35((function(status) {
					var status;
					fail = fail + (1) >> 0;
				})), new funcType$36((function(status) {
					var status;
					progress = progress + (1) >> 0;
				}))])).Done(new sliceType$39([new funcType$37((function() {
					if (pass >= 5) {
						qunit.Start();
						qunit.Ok(new $Bool(pass >= 5 && fail >= 4 && progress >= 20), "Deferred Test 01 fail");
					}
				}))]));
				i = i + (1) >> 0;
			}
			return $ifaceNil;
		}));
		qunit.Test("Deferreds Test 02", (function(assert) {
			var assert, o;
			qunit.Expect(1);
			o = $clone(NewWorking(jquery.NewDeferred()), working);
			o.Deferred.Resolve(new sliceType$2([new $String("John")]));
			o.Deferred.Done(new sliceType$39([new funcType$38((function(name) {
				var name;
				o.hi(name);
			}))])).Done(new sliceType$39([new funcType$39((function(name) {
				var name;
				o.hi("John");
			}))]));
			o.hi("Karl");
			assert.Ok(new $Bool((countJohn === 2) && (countKarl === 1)), "Deferred Test 02 fail");
		}));
		qunit.AsyncTest("Deferreds Test 03", (function() {
			qunit.Expect(1);
			jquery.Get(new sliceType$31([new $String("/get.html")])).Always(new sliceType$40([new funcType$40((function() {
				qunit.Start();
				qunit.Ok(new $Bool(true), "Deferred Test 03 fail");
			}))]));
			return $ifaceNil;
		}));
		qunit.AsyncTest("Deferreds Test 04", (function() {
			qunit.Expect(2);
			jquery.Get(new sliceType$31([new $String("/get.html")])).Done(new sliceType$39([new funcType$41((function() {
				qunit.Ok(new $Bool(true), "Deferred Test 04 fail");
			}))])).Fail(new sliceType$41([new funcType$42((function() {
			}))]));
			jquery.Get(new sliceType$31([new $String("/shouldnotexist.html")])).Done(new sliceType$39([new funcType$43((function() {
			}))])).Fail(new sliceType$41([new funcType$44((function() {
				qunit.Start();
				qunit.Ok(new $Bool(true), "Deferred Test 04 fail");
			}))]));
			return $ifaceNil;
		}));
		qunit.AsyncTest("Deferreds Test 05", (function() {
			qunit.Expect(2);
			jquery.Get(new sliceType$31([new $String("/get.html")])).Then(new sliceType$38([new funcType$45((function() {
				qunit.Ok(new $Bool(true), "Deferred Test 05 fail");
			})), new funcType$46((function() {
			}))]));
			jquery.Get(new sliceType$31([new $String("/shouldnotexist.html")])).Then(new sliceType$38([new funcType$47((function() {
			})), new funcType$48((function() {
				qunit.Start();
				qunit.Ok(new $Bool(true), "Deferred Test 05, 2nd part, fail");
			}))]));
			return $ifaceNil;
		}));
		qunit.Test("Deferreds Test 06", (function(assert) {
			var assert, filtered, o;
			qunit.Expect(1);
			o = $clone(jquery.NewDeferred(), jquery.Deferred);
			filtered = $clone(o.Then(new sliceType$38([new funcType$49((function(value) {
				var value;
				return value * 2 >> 0;
			}))])), jquery.Deferred);
			o.Resolve(new sliceType$2([new $Int(5)]));
			filtered.Done(new sliceType$39([new funcType$50((function(value) {
				var value;
				assert.Ok(new $Bool((value === 10)), "Deferred Test 06 fail");
			}))]));
		}));
		qunit.Test("Deferreds Test 07", (function(assert) {
			var assert, filtered, o;
			o = $clone(jquery.NewDeferred(), jquery.Deferred);
			filtered = $clone(o.Then(new sliceType$38([$ifaceNil, new funcType$51((function(value) {
				var value;
				return value * 3 >> 0;
			}))])), jquery.Deferred);
			o.Reject(new sliceType$3([new $Int(6)]));
			filtered.Fail(new sliceType$41([new funcType$52((function(value) {
				var value;
				assert.Ok(new $Bool((value === 18)), "Deferred Test 07 fail");
			}))]));
		}));
	};
	EvtScenario.methods = [{prop: "Setup", name: "Setup", pkg: "", typ: $funcType([], [], false)}, {prop: "Teardown", name: "Teardown", pkg: "", typ: $funcType([], [], false)}];
	working.methods = [{prop: "notify", name: "notify", pkg: "main", typ: $funcType([], [], false)}, {prop: "hi", name: "hi", pkg: "main", typ: $funcType([$String], [], false)}];
	Object.init($String, $emptyInterface);
	EvtScenario.init([]);
	working.init([{prop: "Deferred", name: "", pkg: "", typ: jquery.Deferred, tag: ""}]);
	$pkg.$init = function() {
		$pkg.$init = function() {};
		/* */ var $r, $s = 0; var $init_main = function() { while (true) { switch ($s) { case 0:
		$r = jquery.$init($BLOCKING); /* */ $s = 1; case 1: if ($r && $r.$blocking) { $r = $r(); }
		$r = js.$init($BLOCKING); /* */ $s = 2; case 2: if ($r && $r.$blocking) { $r = $r(); }
		$r = qunit.$init($BLOCKING); /* */ $s = 3; case 3: if ($r && $r.$blocking) { $r = $r(); }
		$r = strconv.$init($BLOCKING); /* */ $s = 4; case 4: if ($r && $r.$blocking) { $r = $r(); }
		$r = strings.$init($BLOCKING); /* */ $s = 5; case 5: if ($r && $r.$blocking) { $r = $r(); }
		$r = time.$init($BLOCKING); /* */ $s = 6; case 6: if ($r && $r.$blocking) { $r = $r(); }
		jQuery = jquery.NewJQuery;
		countJohn = 0;
		countKarl = 0;
		main();
		/* */ } return; } }; $init_main.$blocking = true; return $init_main;
	};
	return $pkg;
})();
$synthesizeMethods();
$packages["runtime"].$init()();
$go($packages["main"].$init, [], true);
$flushConsole();

}).call(this);
//# sourceMappingURL=index.js.map
