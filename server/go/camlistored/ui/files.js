var lastFiles;

function handleFiles(files) {
    lastFiles = files;

    info = document.getElementById("info");
    t = "N files: " + files.length + "\n";
    for (var i = 0; i < files.length; i++) {
        var file = files[i];
        t += "file[" + i + "] name=" + file.name + "; size=" + file.size + "; file.type=" + file.type + "\n";

        (function(file) {
             var fr = new FileReader();
             fr.onload = function() {
                 dataurl = fr.result;
                 comma = dataurl.indexOf(",")
                 if (comma != -1) {
                     b64 = dataurl.substring(comma + 1);
                     var arrayBuffer = Base64.decode(b64).buffer;
                     var hash = Crypto.SHA1(new Uint8Array(arrayBuffer, 0));
                     info.innerHTML += "File " + file.name + " = sha1-" + hash + "\n";                
                 }
             };
             fr.readAsDataURL(file);
        })(file);
    }
    info.innerHTML += t;
}

function upload(e) {
    if (!e) {
        e = window.event;
    }
    e.stopPropagation();
    e.preventDefault();

    var files = lastFiles;
     if (!files) {
         alert("no files selected");
         return
     }
    alert("files = " + files);
}

function filesOnload() {
    var dnd = document.getElementById("dnd");
    if (!dnd) {
        return;
    }

    stop = function(e) {
        e.stopPropagation();
        e.preventDefault();
    };
    dnd.addEventListener("dragenter", stop, false);
    dnd.addEventListener("dragover", stop, false);

    drop = function(e) {
        stop(e);
        var dt = e.dataTransfer;
        var files = dt.files;
        document.getElementById("info").innerHTML = "";
        handleFiles(files);
    };
    dnd.addEventListener("drop", drop, false);
}
