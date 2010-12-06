all:
	make -C auth install
	make -C httputil install
	make -C blobref install
	make -C webserver install
	make -C openpgp
	make -C blobserver install
	make -C sigserver install

clean:
	make -C auth clean
	make -C httputil clean
	make -C blobref clean
	make -C webserver clean
	make -C openpgp clean
	make -C blobserver clean
	make -C sigserver clean
