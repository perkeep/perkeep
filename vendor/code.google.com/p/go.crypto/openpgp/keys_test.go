package openpgp

import (
	"testing"
	"time"
)

func TestKeyExpiry(t *testing.T) {
	kring, _ := ReadKeyRing(readerFromHex(expiringKeyHex))
	entity := kring[0]

	const timeFormat = "2006-01-02"
	time1, _ := time.Parse(timeFormat, "2013-07-01")
	// The expiringKeyHex key is structured as:
	//
	// pub  1024R/5E237D8C  created: 2013-07-01  expires: 2013-07-31  usage: SC
	// sub  1024R/1ABB25A0  created: 2013-07-01  expires: 2013-07-08  usage: E
	// sub  1024R/96A672F5  created: 2013-07-01  expires: 2013-07-31  usage: E
	//
	// So this should select the first, non-expired encryption key.
	key, _ := entity.encryptionKey(time1)
	if id := key.PublicKey.KeyIdShortString(); id != "1ABB25A0" {
		t.Errorf("Expected key 1ABB25A0 at time %s, but got key %s", time1.Format(timeFormat), id)
	}

	// Once the first encryption subkey has expired, the second should be
	// selected.
	time2, _ := time.Parse(timeFormat, "2013-07-09")
	key, _ = entity.encryptionKey(time2)
	if id := key.PublicKey.KeyIdShortString(); id != "96A672F5" {
		t.Errorf("Expected key 96A672F5 at time %s, but got key %s", time2.Format(timeFormat), id)
	}

	// Once all the keys have expired, nothing should be returned.
	time3, _ := time.Parse(timeFormat, "2013-08-01")
	if key, ok := entity.encryptionKey(time3); ok {
		t.Errorf("Expected no key at time %s, but got key %s", time3.Format(timeFormat), key.PublicKey.KeyIdShortString())
	}
}

const expiringKeyHex = "988d0451d1ec5d010400ba3385721f2dc3f4ab096b2ee867ab77213f0a27a8538441c35d2fa225b08798a1439a66a5150e6bdc3f40f5d28d588c712394c632b6299f77db8c0d48d37903fb72ebd794d61be6aa774688839e5fdecfe06b2684cc115d240c98c66cb1ef22ae84e3aa0c2b0c28665c1e7d4d044e7f270706193f5223c8d44e0d70b7b8da830011010001b40f4578706972792074657374206b657988be041301020028050251d1ec5d021b03050900278d00060b090807030206150802090a0b0416020301021e01021780000a091072589ad75e237d8c033503fd10506d72837834eb7f994117740723adc39227104b0d326a1161871c0b415d25b4aedef946ca77ea4c05af9c22b32cf98be86ab890111fced1ee3f75e87b7cc3c00dc63bbc85dfab91c0dc2ad9de2c4d13a34659333a85c6acc1a669c5e1d6cecb0cf1e56c10e72d855ae177ddc9e766f9b2dda57ccbb75f57156438bbdb4e42b88d0451d1ec5d0104009c64906559866c5cb61578f5846a94fcee142a489c9b41e67b12bb54cfe86eb9bc8566460f9a720cb00d6526fbccfd4f552071a8e3f7744b1882d01036d811ee5a3fb91a1c568055758f43ba5d2c6a9676b012f3a1a89e47bbf624f1ad571b208f3cc6224eb378f1645dd3d47584463f9eadeacfd1ce6f813064fbfdcc4b5a53001101000188a504180102000f021b0c050251d1f06b050900093e89000a091072589ad75e237d8c20e00400ab8310a41461425b37889c4da28129b5fae6084fafbc0a47dd1adc74a264c6e9c9cc125f40462ee1433072a58384daef88c961c390ed06426a81b464a53194c4e291ddd7e2e2ba3efced01537d713bd111f48437bde2363446200995e8e0d4e528dda377fd1e8f8ede9c8e2198b393bd86852ce7457a7e3daf74d510461a5b77b88d0451d1ece8010400b3a519f83ab0010307e83bca895170acce8964a044190a2b368892f7a244758d9fc193482648acb1fb9780d28cc22d171931f38bb40279389fc9bf2110876d4f3db4fcfb13f22f7083877fe56592b3b65251312c36f83ffcb6d313c6a17f197dd471f0712aad15a8537b435a92471ba2e5b0c72a6c72536c3b567c558d7b6051001101000188a504180102000f021b0c050251d1f07b050900279091000a091072589ad75e237d8ce69e03fe286026afacf7c97ee20673864d4459a2240b5655219950643c7dba0ac384b1d4359c67805b21d98211f7b09c2a0ccf6410c8c04d4ff4a51293725d8d6570d9d8bb0e10c07d22357caeb49626df99c180be02d77d1fe8ed25e7a54481237646083a9f89a11566cd20b9e995b1487c5f9e02aeb434f3a1897cd416dd0a87861838da3e9e"
