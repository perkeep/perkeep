all:
	make -C ext/openpgp install
	make -C blobref install
	mkdir -p $(GOROOT)/src/pkg/camli
	mkdir -p $(GOROOT)/src/pkg/camli
	make -C schema install
	make -C client install
	make -C http install
	make -C jsonsign install
	mkdir -p $(GOROOT)/src/pkg/camli
	mkdir -p $(GOROOT)/src/pkg/camli/{blobref,schema,client,http,jsonsign}
	rsync -avPW --delete blobref/ $(GOROOT)/src/pkg/camli/blobref/
	rsync -avPW --delete schema/ $(GOROOT)/src/pkg/camli/schema/
	rsync -avPW --delete client/ $(GOROOT)/src/pkg/camli/client/
	rsync -avPW --delete http/ $(GOROOT)/src/pkg/camli/http/
	rsync -avPW --delete jsonsign/ $(GOROOT)/src/pkg/camli/jsonsign/

clean:
	make -C ext/openpgp clean
	make -C schema clean
	make -C blobref clean
	make -C client clean
	make -C http clean
	make -C jsonsign clean

