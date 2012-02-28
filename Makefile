all:
	./build.pl allfast

clean:
	./build.pl clean

presubmit:
	./build.pl clean
	./build.pl -v -t allfast

embeds:
	go install ./pkg/fileembed/genfileembed/ && genfileembed ./server/camlistored/ui

checkdeps:
	./build.pl --eachclean && echo "SUCCESS"
