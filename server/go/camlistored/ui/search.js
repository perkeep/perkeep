function search() {
    var xhr = new XMLHttpRequest();
    xhr.onreadystatechange = function() {
        if (xhr.readyState != 4) { return; }
        if (xhr.status != 200) {
            console.log("no status 200; got " + xhr.status);
            return;
        }
        document.getElementById("searchres").innerHTML = "<pre>" + linkifyBlobRefs(xhr.responseText) + "</pre>";
    };
    xhr.open("GET", disco.searchRoot + "camli/search", true);
    xhr.send();
}

window.addEventListener("load", search);
