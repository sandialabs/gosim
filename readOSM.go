/*
 * Wandering person simulator
 * This reads in an OpenStreetMaps file, parses the XML, and constructs
 * an internal representation of the streets represented therein.
 * It then has a person randomly walk along the streets.
 */

package main

import (
	"strconv"
	"fmt"
	"os"
	"xml"
	"rand"
	"time"
	"math"
	"./osm"
	"launchpad.net/mgo"
	"launchpad.net/gobson/bson"
)

/* These are the temporary structures into which the XML is parsed */
/* We'll throw them away shortly. */
type Osm struct {
	Version	string	`xml:"attr"`
	Node	[]Node
	Way	[]Way
}

type Node struct {
	XMLName	xml.Name	`xml:"node"`
	Id	string		`xml:"attr"`
	Lat	string		`xml:"attr"`
	Lon	string		`xml:"attr"`
}

type Way struct {
	XMLName	xml.Name	`xml:"way"`
	Id	string		`xml:"attr"`
	Nd	[]Nd
	Tag	[]Tag
}

/* refers to one node in a way */
type Nd struct {
	XMLName	xml.Name	`xml:"nd"`
	Ref	string		`xml:"attr"`
}

type Tag struct {
	XMLName	xml.Name	`xml:"tag"`
	K	string		`xml:"attr"` /* Key */
	V	string		`xml:"attr"` /* Value */
}


// This is a representation of a single person. Probably sub-optimal.
type Person struct {
	Index	int		/* Which person is this? starts at 0 */
	Current	osm.Node	/* Our current location */
	Speed	float64	/* speed in m/s */
	LatSpeed	float64	/* degrees latitude per second */
	LonSpeed float64	/* degrees longitude per second */
	OriginId	uint		/* The ID of the origin in the list of nodes */
	WaypointId	uint	/* ID for the waypoint */
	DestinationId	uint	/* ID of the destination */
	Way		osm.Way	/* The way we're standing on right now */
}

// This is the representation of a Person that actually gets pushed to the database.
// It's simple because we only really care about Latitude and Longitude
type DBPerson struct {
	Index	int
	Lat		float64
	Lon		float64
}

// Read in an XML OSM file and give us back a map of nodes and an array of ways
func ParseOSM(filename string) (result osm.Osm) {
	file, err := os.Open(filename)
	if err != nil {
		fmt.Println(err.String())
	}

	m := new(Osm)
	err = xml.Unmarshal(file, m)
	if err != nil {
		fmt.Println(err.String())
	}

	result.Nodes = make(map[uint]osm.Node)	
	for _, n := range m.Node {
		id, _ := strconv.Atoui(n.Id)
		fmt.Printf("found a node with id %s (%d)\n", n.Id, id)
		lat, _ := strconv.Atof64(n.Lat)
		lon, _ := strconv.Atof64(n.Lon)
		t := osm.NewNode(id, lat, lon)
		result.Nodes[t.Id] = *t
	}

	for _, w := range m.Way {
		wtmp := new(osm.Way)
		wtmp.Id, _ = strconv.Atoui(w.Id)
		for _, nd := range w.Nd {
			node, _ := strconv.Atoui(nd.Ref)
			wtmp.Nodes = append(wtmp.Nodes, node)
		}
		for _, tag := range w.Tag {
			if tag.K == "name" {
				wtmp.Name = tag.V
				fmt.Printf("name = %s\n", wtmp.Name)
			}
			if tag.K == "highway" {
				wtmp.Type = tag.V
			}
		}
		result.Ways = append(result.Ways, *wtmp)
	}

	return
}

func main() {
	streets := ParseOSM("test.osm")

	rand.Seed(time.Nanoseconds() % 1e9)

	session, err := mgo.Mongo("localhost")
	if err != nil { panic(err) } 

	defer session.Close()
    num_people := 50
	done := make([]chan int, 1, num_people)
	for i := 0; i < num_people; i++ {
		done = append(done, make(chan int))

		go Wander(session, i, streets, done[i])
	}
	<-done[0]
}


// This function expects an open session connected to a MongoDB server, 
// a number (basically an index of this person, for logging purposes), 
// a map of the nodes, an array of ways, and then a channel to report 
// back over if/when it ever finishes (tip: as of now, it won't)
func Wander(session *mgo.Session, num int, streets osm.Osm, done chan int) {
	c := session.DB("megadroid").C("phones")
    c.DropCollection()

	// Pick a random way
	whichway := uint(rand.Intn(len(streets.Ways)))
	w := streets.Ways[whichway]

	// Pick a random node on that way -- this is where we start
	tmp := uint(rand.Intn(len(w.Nodes)))
	curr := w.Nodes[tmp]

	fmt.Printf("#%d: selected way #%d out of %d, which is %v\n", num, whichway, len(streets.Ways), w.Name)

	fmt.Printf("#%d: Standing on node #%d.\n", num, curr)

	p := new(Person)
	p.OriginId = curr
	p.Speed = 1.0
	p.Index = num

	for {
		// Figure out what ways go through this node
		intersect := osm.FindWays(streets.Ways, p.OriginId)

		// Pick one of these ways at random
		whichway = uint(rand.Intn(len(intersect)))
		p.Way = intersect[whichway]

		fmt.Printf("#%d: taking %v\n", p.Index, p.Way.Name)

		// Look through the list of nodes until we find the correct index
		var startidx uint
		for i, _ := range p.Way.Nodes {
			if p.Way.Nodes[i] == p.OriginId {
				startidx = uint(i)
				break
			}
		}

		fmt.Printf("#%d: our starting index in this way is %d, which points to node #%d\n", p.Index, startidx, p.Way.Nodes[startidx])

		// Set the current node
		p.OriginId = p.Way.Nodes[startidx]

		// Pick a node from that way for us to go to
		destidx := uint(rand.Intn(len(p.Way.Nodes)))
		// our destination shouldn't be the same as the start
		for ;destidx == startidx; destidx = uint(rand.Intn(len(p.Way.Nodes))) {
		}
		p.DestinationId = p.Way.Nodes[destidx]

		// How far away is that node?
		fmt.Printf("#%d: selected node #%d, which is %v meters away\n", p.Index, p.DestinationId, osm.GetDist(streets.Nodes[p.OriginId], streets.Nodes[p.DestinationId]))

		// Move to that node!
		nextidx := startidx
		for {
			// Which way do we traverse the list?
			if destidx > startidx {
				nextidx = startidx + 1
			} else if destidx < startidx {
				nextidx = startidx - 1
			} else {
				// TODO: we're there! handle this later
				break
			}

			p.WaypointId = p.Way.Nodes[nextidx]

			p.UpdateLatLonSpeed(streets.Nodes[p.OriginId], streets.Nodes[p.WaypointId])
			p.Current = *osm.NewNode(0, streets.Nodes[p.OriginId].Lat, streets.Nodes[p.OriginId].Lon)

			_, err := c.Upsert(bson.M{"index": p.Index}, &DBPerson{p.Index, p.Current.Lat, p.Current.Lon}) 
			if err != nil { panic(err) }

			fmt.Printf("#%d: Waypoint is %v meters away from our current node\n", p.Index, osm.GetDist(p.Current, streets.Nodes[p.WaypointId]))

			for ; osm.GetDist(p.Current, streets.Nodes[p.WaypointId]) > p.Speed; {
				fmt.Printf("#%d: location = %v by %v, This is %v meters from the waypoint\n", p.Index, p.Current.Lat, p.Current.Lon, osm.GetDist(p.Current, streets.Nodes[p.WaypointId]))
				p.Current = *osm.NewNode(0, p.Current.Lat + p.LatSpeed, p.Current.Lon + p.LonSpeed)
				_, err := c.Upsert(bson.M{"index": p.Index}, &DBPerson{p.Index, p.Current.Lat, p.Current.Lon}) 
				if err != nil { panic(err) }
				time.Sleep(50000000)
			}

			startidx = nextidx
			p.OriginId = p.Way.Nodes[startidx]

			fmt.Printf("#%d: Next waypoint set to #%d, which is %v meters from the destination\n\n", p.Index, p.Way.Nodes[nextidx], osm.GetDist(streets.Nodes[p.WaypointId], streets.Nodes[p.DestinationId]))
		}
		fmt.Printf("\n**************\n")
		// You must be tired, take a break!
		time.Sleep(5000000000)
	}
}

// This function sets a Person's speed as a vector from Node n1 to Node n2
// This will probably be problematic if n1 and n2 are really far apart.
func (p *Person) UpdateLatLonSpeed(n1, n2 osm.Node) {
	const R float64 = 6371000
	x := ((math.Pi/180)*(n2.Lon - n1.Lon))*math.Cos((math.Pi/180)*n1.Lat)*R
	y := (math.Pi/180)*(n2.Lat - n1.Lat)*R

	theta := math.Atan2(y, x)

	vx := p.Speed*math.Cos(theta)
	vy := p.Speed*math.Sin(theta)

	p.LatSpeed = (vy / R)*(180/math.Pi)
	p.LonSpeed = (vx / (R * math.Cos((math.Pi/180)*n1.Lat)))*(180/math.Pi)
}
