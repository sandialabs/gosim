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

/* An OpenStreetMaps map */
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

type DBPerson struct {
	Index	int
	Lat		float64
	Lon		float64
}

func ParseOSM(filename string) (nodes map[uint]osm.Node, ways []osm.Way) {
	file, err := os.Open(filename)
	if err != nil {
		fmt.Println(err.String())
	}

	m := new(Osm)
	err = xml.Unmarshal(file, m)
	if err != nil {
		fmt.Println(err.String())
	}

	nodes = make(map[uint]osm.Node)	
	for _, n := range m.Node {
		id, _ := strconv.Atoui(n.Id)
		fmt.Printf("found a node with id %s (%d)\n", n.Id, id)
		lat, _ := strconv.Atof64(n.Lat)
		lon, _ := strconv.Atof64(n.Lon)
		t := osm.NewNode(id, lat, lon)
		nodes[t.Id] = *t
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
		ways = append(ways, *wtmp)
	}

	return
}

func main() {

	/* This is initialization stuff */
	nodes, ways := ParseOSM("test.osm")

	rand.Seed(time.Nanoseconds() % 1e9)

	session, err := mgo.Mongo("localhost")
	if err != nil { panic(err) } 

//	defer session.Close()

	done := make([]chan int, 1, 100)
	for i := 0; i < 100; i++ {
		done = append(done, make(chan int))

		go Wander(session, i, nodes, ways, done[i])
	}
	<-done[0]
}

func Wander(session *mgo.Session, num int, nodes map[uint]osm.Node, ways []osm.Way, done chan int) {
	c := session.DB("test").C("people")

	// Pick a random way
	whichway := uint(rand.Intn(len(ways)))
	w := ways[whichway]

	// Pick a random node on that way -- this is where we start
	tmp := uint(rand.Intn(len(w.Nodes)))
	curr := w.Nodes[tmp]

	fmt.Printf("#%d: selected way #%d out of %d, which is %v\n", num, whichway, len(ways), w.Name)

	fmt.Printf("#%d: Standing on node #%d.\n", num, curr)

	dude := new(Person)
	dude.OriginId = curr
	dude.Speed = 1.0
	dude.Index = num

	for {
		/* This will end up being the main navigation loop, I think */
		// Figure out what ways go through this node
		intersect := osm.FindWays(ways, curr)
//		for _, w = range intersect {
//			fmt.Printf("* %s\n", w.Name)
//		}

		// Pick one of these ways at random
		whichway = uint(rand.Intn(len(intersect)))
		dude.Way = intersect[whichway]

		fmt.Printf("#%d: taking %v\n", dude.Index, dude.Way.Name)

		// Look through the list of nodes until we find the correct index
		var startidx uint
		for i, _ := range dude.Way.Nodes {
			if dude.Way.Nodes[i] == curr {
				startidx = uint(i)
				break
			}
		}

		fmt.Printf("#%d: our starting index in this way is %d, which points to node #%d\n", dude.Index, startidx, dude.Way.Nodes[startidx])

		// Set the current node
		dude.OriginId = dude.Way.Nodes[startidx]

		// Pick a node from that way for us to go to
		destidx := uint(rand.Intn(len(dude.Way.Nodes)))
		// our destination shouldn't be the same as the start
		for ;destidx == startidx; destidx = uint(rand.Intn(len(dude.Way.Nodes))) {
		}
		dude.DestinationId = dude.Way.Nodes[destidx]

		// How far away is that node?
		fmt.Printf("#%d: selected node #%d, which is %v meters away\n", dude.Index, dude.DestinationId, osm.GetDist(nodes[dude.OriginId], nodes[dude.DestinationId]))

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

			dude.WaypointId = dude.Way.Nodes[nextidx]

			dude.UpdateLatLonSpeed(nodes[dude.OriginId], nodes[dude.WaypointId])
			dude.Current = *osm.NewNode(0, nodes[dude.OriginId].Lat, nodes[dude.OriginId].Lon)

			_, err := c.Upsert(bson.M{"index": dude.Index}, &DBPerson{dude.Index, dude.Current.Lat, dude.Current.Lon}) 
			if err != nil { panic(err) }

			fmt.Printf("#%d: Waypoint is %v meters away from our current node\n", dude.Index, osm.GetDist(dude.Current, nodes[dude.WaypointId]))

			for ; osm.GetDist(dude.Current, nodes[dude.WaypointId]) > dude.Speed; {
				fmt.Printf("#%d: location = %v by %v, This is %v meters from the waypoint\n", dude.Index, dude.Current.Lat, dude.Current.Lon, osm.GetDist(dude.Current, nodes[dude.WaypointId]))
				dude.Current = *osm.NewNode(0, dude.Current.Lat + dude.LatSpeed, dude.Current.Lon + dude.LonSpeed)
				_, err := c.Upsert(bson.M{"index": dude.Index}, &DBPerson{dude.Index, dude.Current.Lat, dude.Current.Lon}) 
				if err != nil { panic(err) }
				time.Sleep(1000000000)
			}

			startidx = nextidx
			dude.OriginId = dude.Way.Nodes[startidx]

			fmt.Printf("#%d: Next waypoint set to #%d, which is %v meters from the destination\n\n", dude.Index, dude.Way.Nodes[nextidx], osm.GetDist(nodes[dude.WaypointId], nodes[dude.DestinationId]))
		}
		fmt.Printf("\n**************\n")
		time.Sleep(5000000000)
	}
}

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