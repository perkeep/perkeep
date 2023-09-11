variables {
  storage_root = env.PERKEEP_TEST_HOME
}

network {
  https      = true
  https_cert = "server.cert"
  https_key  = "server.key"
  listen     = "0.0.0.0:${env.PERKEEP_TEST_LISTEN_PORT}"

  auth "basic" {
    username = env.PERKEEP_TEST_USERNAME
    password = env.PERKEEP_TEST_PASSWORD
  }
}

server {
  blob_source = storage.blobpacked
  data_dir = "${var.storage_root}/perkeepd"

  // TODO: this should be a block with attributes "key_id" and "secret_ring"
  identity = {
    id: "F120C18FF0D9EFA5",
    keyring: "${var.storage_root}/perkeepd/identity-secring.gpg"
  }

  // TODO: auth blocks here, .. eventually maybe also an index, sync, index, cache block
}

storage "blobpacked" {
  small_blobs = { type : "localdisk", path : "${var.storage_root}/blobpacked/small" }
  large_blobs = { type : "localdisk", path : "${var.storage_root}/blobpacked/large" }
  meta_index  = { type : "sqlite", file : "${var.storage_root}/blobpacked/index.sqlite" }
}
