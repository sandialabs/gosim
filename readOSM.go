/*
 * Wandering person simulator
 * This reads in an OpenStreetMaps file, parses the XML, and constructs
 * an internal representation of the streets represented therein.
 * It then has a person randomly walk along the streets.
 */

package main

import (
	"bufio"
	"strconv"
	"fmt"
	"os"
	"xml"
	"rand"
	"time"
	"math"
	"net"
	"./osm"
	"strings"
	"launchpad.net/mgo"
	"launchpad.net/gobson/bson"
)

var (
	done	map[string]chan int
	handlers	map[string]func(*net.TCPConn, []string)()
	people	map[string]*Person
	streets	osm.Osm
	session	*mgo.Session
)

const (
	STOP = iota
	CONTINUE
	PAUSE
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
	UID	string		/* Which android device is this? Should be unique.*/
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
	UID	string
	Lat		float64
	Lon		float64
}

// Read in an XML OSM file and give us back a map of nodes and an array of ways
func ParseOSM(filename string) (result osm.Osm) {
	file, err := os.Open(filename)
	if err != nil {
		fmt.Println(err.String())
	}
	defer file.Close()

	m := new(Osm)
	err = xml.Unmarshal(file, m)
	if err != nil {
		fmt.Println(err.String())
	}

	result.Nodes = make(map[uint]osm.Node)	
	for _, n := range m.Node {
		id, _ := strconv.Atoui(n.Id)
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
			}
			if tag.K == "highway" {
				wtmp.Type = tag.V
			}
		}
		result.Ways = append(result.Ways, *wtmp)
	}

	return
}

func init() {
	handlers = map[string]func(*net.TCPConn, []string)(){ "Tpos":HandlePos,
											"Tstart":HandleStart,
											"Tstop":HandleStop,
											"Tpause":HandlePause,
											"Tcontinue":HandleCont }

	done = make(map[string]chan int)
	people = make(map[string]*Person)
}

func main() {
	var err os.Error
	streets = ParseOSM(os.Args[1])

	rand.Seed(time.Nanoseconds() % 1e9)

	session, err = mgo.Mongo("localhost")
	if err != nil { panic(err) } 
	defer session.Close()
	c := session.DB("megadroid").C("phones")
	c.DropCollection()

	addr, _ := net.ResolveTCPAddr("tcp", ":4001")
	l, err := net.ListenTCP("tcp", addr)
	if err != nil { panic(err) }

	for {
		c, _ := l.AcceptTCP()
		fmt.Printf("accepted call from %v\n", c.RemoteAddr())
		go Handler(c)
	}
}

func readline(b *bufio.Reader) (p []byte, err os.Error) {
	if p, err = b.ReadSlice('\n'); err != nil {
		return nil, err;
	}
	var i int;
	for i = len(p); i > 0; i-- {
		if c := p[i-1]; c != '\r' && c != '\n' {
			break;
		}
	}
	return p[0:i], nil;
}

func HandleStart(c *net.TCPConn, args []string) () {
	uid := args[0]

	fmt.Printf("Creating new wanderer with ID = %v\n", uid)
	if done[uid] == nil {
		done[uid] = make(chan int)
		pchan := make(chan *Person)
		go Wander(session, uid, streets, pchan, done[uid])
		people[uid] = <-pchan
	}
	fmt.Fprintf(c, "Rstart %s\n", uid)
}

func Rerror(c *net.TCPConn) {
	fmt.Fprintf(c, "Rerror\n")
}

func HandlePos(c *net.TCPConn, args []string) () {
	if args != nil {
		db := session.DB("megadroid").C("phones")
		fmt.Printf("arguments = %v\n", args)
		uid := args[0]
		result := &DBPerson{}
		_ = db.Find(bson.M{"uid": uid}).One(&result)
		fmt.Fprintf(c, "Rpos %s %f %f\n", result.UID, result.Lat, result.Lon)
	} else {
		Rerror(c)
	}
}

func HandleStop(c *net.TCPConn, args []string) () {
	if args != nil {
		uid := args[0]
		done[uid]<- STOP
		fmt.Fprintf(c, "Rstop %s\n", uid)
		done[uid] = nil
		people[uid] = nil
	} else {
		Rerror(c)
	}
}

func HandlePause(c *net.TCPConn, args []string) () {
	if args != nil {
		done[args[0]]<- PAUSE
		fmt.Fprintf(c, "Rpause %s\n", args[0])
	} else {
		Rerror(c)
	}
}

func HandleCont(c *net.TCPConn, args []string) () {
	if args != nil {
		done[args[0]]<- CONTINUE
		fmt.Fprintf(c, "Rcontinue %s\n", args[0])
	} else {
		Rerror(c)
	}
}

func Handler(c *net.TCPConn) {
	br := bufio.NewReader(c)
	defer c.Close()

	for {
		line, err := readline(br)
		if err != nil { fmt.Print("exiting\n"); return }

		fmt.Printf("read %#v\n", string(line))
		tok := strings.Split(string(line), " ")
		fmt.Printf("%#v\n", tok)

		//handlers[tok[0]](tok[1:])
		h := handlers[tok[0]]
		if h != nil {
			h(c, tok[1:])
		} else {
			fmt.Fprintf(c, "Rerror\n")
		}
	}
}

// This function expects an open session connected to a MongoDB server, 
// a number (basically an index of this person, for logging purposes), 
// a map of the nodes, an array of ways, and then a channel to report 
// back over if/when it ever finishes (tip: as of now, it won't)
func Wander(session *mgo.Session, uid string, streets osm.Osm, pchan chan *Person, done chan int) {
	c := session.DB("megadroid").C("phones")

	// Pick a random way
	whichway := uint(rand.Intn(len(streets.Ways)))
	w := streets.Ways[whichway]

	// Pick a random node on that way -- this is where we start
	tmp := uint(rand.Intn(len(w.Nodes)))
	curr := w.Nodes[tmp]

	fmt.Printf("#%v: selected way #%d out of %d, which is %v\n", uid, whichway, len(streets.Ways), w.Name)

	fmt.Printf("#%v: Standing on node #%d.\n", uid, curr)

	p := new(Person)
	p.OriginId = curr
	p.Speed = 5.0
	p.UID = uid

	pchan<- p

	for {
		// Figure out what ways go through this node
		intersect := osm.FindWays(streets.Ways, p.OriginId)

		// Pick one of these ways at random
		whichway = uint(rand.Intn(len(intersect)))
		p.Way = intersect[whichway]

		fmt.Printf("#%v: taking %v\n", p.UID, p.Way.Name)

		// Look through the list of nodes until we find the correct index
		var startidx uint
		for i, _ := range p.Way.Nodes {
			if p.Way.Nodes[i] == p.OriginId {
				startidx = uint(i)
				break
			}
		}

		fmt.Printf("#%v: our starting index in this way is %d, which points to node #%d\n", p.UID, startidx, p.Way.Nodes[startidx])

		// Set the current node
		p.OriginId = p.Way.Nodes[startidx]

		// Pick a node from that way for us to go to
		destidx := uint(rand.Intn(len(p.Way.Nodes)))
		// our destination shouldn't be the same as the start
		for ;destidx == startidx; destidx = uint(rand.Intn(len(p.Way.Nodes))) {
		}
		p.DestinationId = p.Way.Nodes[destidx]

		// How far away is that node?
		fmt.Printf("#%v: selected node #%d, which is %v meters away\n", p.UID, p.DestinationId, osm.GetDist(streets.Nodes[p.OriginId], streets.Nodes[p.DestinationId]))

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

			_, err := c.Upsert(bson.M{"uid": p.UID}, &DBPerson{p.UID, p.Current.Lat, p.Current.Lon}) 
			if err != nil { panic(err) }

			fmt.Printf("#%v: Waypoint is %v meters away from our current node\n", p.UID, osm.GetDist(p.Current, streets.Nodes[p.WaypointId]))

			for ; osm.GetDist(p.Current, streets.Nodes[p.WaypointId]) > p.Speed; {
				select {
				case verb := <-done:
					if verb == STOP {
						fmt.Printf("#%v: exiting\n", p.UID)
						c.Remove(bson.M{"uid":p.UID})
						return
					} else if verb == PAUSE {
						for {
							verb = <-done
							if verb == CONTINUE {
								break
							} else if verb == STOP {
								fmt.Printf("#%v: exiting\n", p.UID)
								return
							}
						}
					}
				default: 
					fmt.Printf("#%v: location = %v by %v, This is %v meters from the waypoint\n", p.UID, p.Current.Lat, p.Current.Lon, osm.GetDist(p.Current, streets.Nodes[p.WaypointId]))
					p.Current.Lat = p.Current.Lat + p.LatSpeed
					p.Current.Lon = p.Current.Lon + p.LonSpeed
					_, err := c.Upsert(bson.M{"uid": p.UID}, &DBPerson{p.UID, p.Current.Lat, p.Current.Lon}) 
					if err != nil { panic(err) }
					time.Sleep(1000000000)
				}
			}

			startidx = nextidx
			p.OriginId = p.Way.Nodes[startidx]

			fmt.Printf("#%v: Next waypoint set to #%d, which is %v meters from the destination\n\n", p.UID, p.Way.Nodes[nextidx], osm.GetDist(streets.Nodes[p.WaypointId], streets.Nodes[p.DestinationId]))
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
