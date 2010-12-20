all:
	make -C openpgp
	make -C ../../lib/go/blobref install
	make -C ../../lib/go/jsonsign install
	make -C auth install
	make -C httputil install
	make -C webserver install
	make -C blobserver install
	make -C sigserver install

clean:
	make -C openpgp clean
	make -C ../../lib/go/blobref clean
	make -C ../../lib/go/jsonsign clean
	make -C auth clean
	make -C httputil clean
	make -C webserver clean
	make -C blobserver clean
	make -C sigserver clean
