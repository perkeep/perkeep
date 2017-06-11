$global.$jsbuiltin$ = {
    typeoffunc: function(x) { return typeof x },
    instanceoffunc: function(x,y) { return x instanceof y },
    infunc: function(x,y) { return x in y }
}
