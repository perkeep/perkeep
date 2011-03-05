all:
	./build.pl allfast

clean:
	./build.pl clean

presubmit:
	./build.pl clean
	./build.pl -v -t allfast

checkdeps:
	./build.pl --eachclean && echo "SUCCESS"
