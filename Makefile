all:
	./build.pl all

clean:
	./build.pl clean

checkdeps:
	./build.pl --eachclean && echo "SUCCESS"
