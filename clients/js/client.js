var Camli = {

BlobStore: function() {
}

};

Camli.BlobStore.prototype.blobURL = function(ref) {
  return '/camli/' + ref;
};

Camli.BlobStore.prototype.xhr = function(url, cb) {
  var xhr = new XMLHttpRequest();
  xhr.onreadystatechange = function() {
    if (xhr.readyState == 4) {
      if (xhr.status == 200) {
        cb(xhr.responseText);
      }
    }
    // XXX handle error
  };
  xhr.open('GET', url, true);
  xhr.send(null);
};

Camli.BlobStore.prototype.xhrJSON = function(url, cb) {
  this.xhr('/camli/enumerate-blobs', function(data) {
    cb(JSON.parse(data));
  });
};

Camli.BlobStore.prototype.enumerate = function(cb) {
  this.xhrJSON('/camli/enumerate-blobs', cb);
};

Camli.BlobStore.prototype.getBlob = function(ref, cb) {
  this.xhr(this.blobURL(ref), cb);
};
