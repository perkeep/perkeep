from setuptools import setup
setup(
    name='camlistore-client',
    version='1.0.3dev',
    author='Brett Slatkin',
    author_email='bslatkin@gmail.com',
    maintainer='Jack Laxson',
    maintainer_email='jackjrabbit+camli@gmail.com',
    description="Client library for Camlistore.",
    url='http://camlistore.org',
    license='Apache v2',
    long_description='A convience library for python developers wishing to explore camlistore.',
    packages=['camli'],
    install_requires=['simplejson'],
    classifiers=['Environment :: Console', 'Topic :: Internet :: WWW/HTTP']
)