The All People's Simulator
===============================

This is a simple simulation of people wandering around a city. It
takes an Open Street Maps file (provided by you) and then allows you
to place simulated people on the map, who then wander around randomly.

Installation
--------------

Download the code, then run "go build ."

You may need to download the external libraries:

* launchpad.net/mgo
* github.com/floren/ellipsoid

You will need to be running mongodb somewhere.

Configuration
--------------

gosim.config contains a sample configuration. This should work fine if you're running mongodb on the local machine. Otherwise, you may need to tweak things. The following are the options which can be set:

* MongoServer: The server running MongoDB.
* Database: The name of the database to use on MongoDB
* Collection: The name of the collection we will use inside the specified database
* ListenPort: The local port where we will listen for incoming connections to control the simulation

By default, gosim reads "gosim.config" in the current directory. To specify a different config, use the "--config" flag.

Preparing an OSM file
----------------------

You can download sections of an Open Street Maps map in xml format
from their website. However, this file needs some processing (using
Osmosis) before you can use it with APS. Here's what I did to select a
subset from the San Francisco Bay Area file available on their
website:

	cat sf-bay-area.osm | osmosis-0.39/bin/osmosis --read-xml enableDateParsing=no file=- --bounding-box top=37.8125 left=-122.5216 bottom=37.7253 right=-122.3826 --tf accept-ways highway=* --tf reject-ways railway=* --tf reject-relations --used-node --write-xml file=downtown-sf.osm

For other files, you will want to leave off the " --bounding-box
top=37.8125 left=-122.5216 bottom=37.7253 right=-122.3826" parameters,
or replace them with your own.

Running
--------

A test OSM file is provided, called "test.osm". To run, simply do this:

	./gosim test.osm

The simulator will read in the OSM file and wait. You can then connect to localhost:4001 and issue commands. The following commands (called "T-messages") are accepted:

	* Tstart <name>: start a new wandering person called <name>
	* Tstop <name>: stop the specified person and remove from the list.
	* Tpause <name>: pause the specified person.
	* Tcontinue <name>: resume movement for a paused person.

A successfully executed T-message will receive an R-message in reply; for example, sending "Tstart john" will cause the server to send back "Rstart john". An invalid command will return an Rerror.

Once a person has been added, the simulator will simulate a random walking pattern for that person, updating his latitude/longitude in the MongoDB database, under a database named "megadroid" and a collection named "phones". You can confirm correct operation like this:

	% mongo
	MongoDB shell version: 2.0.2
	connecting to: test
	> use megadroid
	switched to db megadroid
	> db.phones.find()
	{ "_id" : ObjectId("4f6a5afd2f62ecc1c3f57ed4"), "uid" : "john", "lat" : 37.67995017693356, "lon" : -121.75102342177308 }
	> 

These positions can then be extracted for further processing or display.

Two files, "launch-tests" and "launch-many-tests", can be used to simplify the process of launching lots of test wanderers. Simply run "telnet localhost 4001 < launch-tests" to start 100 simulated people. launch-many-tests will start 500 simulated people.

