
var ports = [40001, 40002, 40011, 40012, 40013, 40021, 40022, 40023, 40101, 40201, 40202]

for (var i = 0; i != ports.length; i++) {
    var server = "localhost:" + ports[i]
    var mongo = new Mongo("localhost:" + ports[i])
    var admin = mongo.getDB("admin")
    if (ports[i] == 40002) {
        admin.auth("root", "rapadura")
    }
    var result = admin.runCommand({"listDatabases": 1})
    // Why is the command returning undefined!?
    while (typeof result.databases == "undefined") {
        print("dropall.js: listing databases got:", result)
        result = admin.runCommand({"listDatabases": 1})
    }
    var dbs = result.databases
    for (var j = 0; j != dbs.length; j++) {
        var db = dbs[j]
        switch (db.name) {
        case "admin":
        case "local":
        case "config":
            break
        default:
            mongo.getDB(db.name).dropDatabase()
        }
    }
}

// vim:ts=4:sw=4:et
