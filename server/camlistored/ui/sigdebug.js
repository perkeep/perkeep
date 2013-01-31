var sigdisco = null;

function discoverJsonSign() {
    var xhr = new XMLHttpRequest();
    xhr.onreadystatechange = function() {
        if (xhr.readyState != 4) { return; }
        if (xhr.status != 200) {
            console.log("no status 200; got " + xhr.status);
            return;
        }
        sigdisco = JSON.parse(xhr.responseText);
        document.getElementById("sigdiscores").innerHTML = "<pre>" + JSON.stringify(sigdisco, null, 2) + "</pre>";
    };
    xhr.open("GET", Camli.config.jsonSignRoot + "/camli/sig/discovery", true);
    xhr.send();
}

function addKeyRef() {
    if (!sigdisco) {
        alert("must do jsonsign discovery first");        
        return;
    }
    clearta = document.getElementById("clearjson");
    var j;
    try {
        j = JSON.parse(clearta.value);
    } catch (x) {
        alert(x);
        return
    }
    j.camliSigner = sigdisco.publicKeyBlobRef;
    clearta.value = JSON.stringify(j);
}

function doSign() {
    if (!sigdisco) {
        alert("must do jsonsign discovery first");
        return;
    }
    clearta = document.getElementById("clearjson");

    var xhr = new XMLHttpRequest();
    xhr.onreadystatechange = function() {
        if (xhr.readyState != 4) { return; }
        if (xhr.status != 200) {
            alert("got status " + xhr.status)
            return;
        }
        document.getElementById("signedjson").value = xhr.responseText;
    };
    xhr.open("POST", sigdisco.signHandler, true);
    xhr.setRequestHeader("Content-Type", "application/x-www-form-urlencoded");
    xhr.send("json=" + encodeURIComponent(clearta.value));
}

function doVerify() {
    if (!sigdisco) {
        alert("must do jsonsign discovery first");
        return;
    }

    signedta = document.getElementById("signedjson");

    var xhr = new XMLHttpRequest();
    xhr.onreadystatechange = function() {
        if (xhr.readyState != 4) { return; }
        if (xhr.status != 200) {
            alert("got status " + xhr.status)
            return;
        }
        document.getElementById("verifyinfo").innerHTML = "<pre>" + xhr.responseText + "</pre>";
    };
    xhr.open("POST", sigdisco.verifyHandler, true);
    xhr.setRequestHeader("Content-Type", "application/x-www-form-urlencoded");
    xhr.send("sjson=" + encodeURIComponent(signedta.value));
}
