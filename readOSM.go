/*
 * Wandering person simulator
 * This reads in an OpenStreetMaps file, parses the XML, and constructs
 * an internal representation of the streets represented therein.
 * It then has a person randomly walk along the streets.
 */

package main

import (
	"log"
	"bufio"
	"fmt"
	"os"
	"io"
	"xml"
	"rand"
	"time"
	"net"
	"strings"
	"launchpad.net/mgo"
	"launchpad.net/gobson/bson"
	"github.com/npe9/ellipsoid"
)

var (
	handlers	map[string]func(*net.TCPConn, []string)()
	people	map[string]*Person
	session	*mgo.Session
	osm		*Osm

	randomizer *rand.Rand
	geo ellipsoid.Ellipsoid
	newPerson chan *Person
)

const (
	STOP = iota
	CONTINUE
	PAUSE
)

type Nd struct {
	Ref int `xml:"attr"`
}

type Tag struct {
	K string `xml:"attr"`
	V string `xml:"attr"`
}

type Way struct {
	Nd   []Nd
	Node []*Node
	Tag  []*Tag
}

func (w *Way) RandNode() Node {
	return *w.Node[randomizer.Intn(len(w.Node))]
}

func (w *Way) Nodes() []*Node {
	return w.Node
}

type Relation struct {

}

type Node struct {
	Id  int     `xml:"attr"`
	Lat float64 `xml:"attr"`
	Lon float64 `xml:"attr"`
	Way []*Way
}

func (n Node) RandWay() *Way {
	return n.Way[randomizer.Intn(len(n.Way))]
}

func (n Node) Distance(m Node) (float64, float64) {
	return geo.To(n.Lat, n.Lon, m.Lat, m.Lon)
}

func (n Node) Move(dist, bearing float64) (m Node) {
	m.Lat, m.Lon = geo.At(n.Lat, n.Lon, dist, bearing)
	return
}

func (n Node) Eq(m Node) bool {
	// XXX: probably need to be allow fuzziness here
	return n.Lat == m.Lat && n.Lon == m.Lon
}

type Osm struct {
	XMLName  xml.Name `xml:"osm"`
	Node     []*Node
	Way      []*Way
	Relation []*Relation
}

func NewOsmXml(rd io.Reader) (o *Osm, err os.Error) {
	err = xml.Unmarshal(rd, &o)
	if err != nil {
		return
	}
	idmap := make(map[int]*Node)
	for _, n := range o.Node {
		fmt.Printf("got node %#v\n", n)
		idmap[n.Id] = n
	}
	for _, w := range o.Way {
		w.Node = make([]*Node, len(w.Nd))
		for i, nd := range w.Nd {
			n := idmap[nd.Ref]
			w.Node[i] = n
			n.Way = append(n.Way, w)
		}
	}

	return
}

func (o *Osm) RandNode() Node {
	return *o.Node[randomizer.Intn(len(o.Node))]
}

func (o *Osm) RandWay(n *Node) *Way {
	return n.Way[randomizer.Intn(len(n.Way))]
}

// This is a representation of a single person. Probably sub-optimal.
type Person struct {
	UID	string		/* Which android device is this? Should be unique.*/
	loc	Node
	dest	Node
	bearing	float64
	way	*Way
//	Current	osm.Node	/* Our current location */
//	Speed	float64	/* speed in m/s */
//	LatSpeed	float64	/* degrees latitude per second */
//	LonSpeed float64	/* degrees longitude per second */
//	OriginId	uint		/* The ID of the origin in the list of nodes */
//	WaypointId	uint	/* ID for the waypoint */
//	DestinationId	uint	/* ID of the destination */
//	Way		osm.Way	/* The way we're standing on right now */
}

func NewPerson(uid string, n Node) (p Person) {
	return Person{UID: uid, loc: n}
}

func (p *Person) Move(dist float64) {
	// what happens with zero value?
	p.SetLoc(p.loc.Move(dist, p.bearing))
	fmt.Printf("%#v\n", p)
	// 1 meter accuracy
	if dist, _ := p.loc.Distance(p.dest); dist < 1.0 {
		p.loc = p.dest
		// XXX: this needs to be setup in the way to allow it to pop a node
//		p.dest = *p.way.Nodes()[0]
//		p.way.Nodes()[1:]
	}
}

func (p *Person) Loc() Node {
	return p.loc
}

func (p *Person) SetLoc(n Node) {
	p.loc = n
}

func (p *Person) SetWay(w *Way) {
	p.way = w
}

func (p *Person) SetDest(n Node) {
	p.dest = n
}


// This is the representation of a Person that actually gets pushed to the database.
// It's simple because we only really care about Latitude and Longitude
type DBPerson struct {
	UID	string
	Lat		float64
	Lon		float64
}

func init() {
	handlers = map[string]func(*net.TCPConn, []string)(){ "Tpos":HandlePos,
											"Tstart":HandleStart,
											"Tstop":HandleStop,
											"Tpause":HandlePause,
											"Tcontinue":HandleCont }

	randomizer = rand.New(rand.NewSource(time.Nanoseconds()))
	geo = ellipsoid.Init("WGS84", ellipsoid.Degrees, ellipsoid.Meter, ellipsoid.Longitude_is_symmetric, ellipsoid.Bearing_is_symmetric)
	// person
	newPerson = make(chan *Person)
	people = make(map[string]*Person)
	ticker := time.NewTicker(5e8)
	go func() {
		for {
			select {
			case p := <-newPerson:
				fmt.Printf("read %v off of the channel\n", p)
				people[p.UID] = p
				fmt.Printf("got a new person %v\n", p)
			case <-ticker.C:
				fmt.Printf("tick\n")
				for _, p := range people {
					// if person 
					// average person (male) walks 1.56464 m/s
					p.Move(1.56464)
				}
				// XXX: we should have some way to finish this
				// case id := <-done:
				// 	people[id] = nil, nil
			}
		}
	}()

//	done = make(map[string]chan int)

}

func main() {
	var err os.Error
	f, err := os.Open(os.Args[1])
	defer f.Close()
	if err != nil {
		log.Fatal(err)
	}

	osm, err = NewOsmXml(f)

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
	p := NewPerson(uid, osm.RandNode())
	p.SetWay(p.Loc().RandWay())
	// Decide where to go
	p.SetDest((p.Loc().RandWay()).RandNode())
	fmt.Printf("pushing %v down the pipe\n", p)
	newPerson <- &p
//	if done[uid] == nil {
//		done[uid] = make(chan int)
//		pchan := make(chan *Person)
//		go Wander(session, uid, streets, pchan, done[uid])
//		people[uid] = <-pchan
//	}
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
//		done[uid]<- STOP
		fmt.Fprintf(c, "Rstop %s\n", uid)
//		done[uid] = nil
		people[uid] = nil
	} else {
		Rerror(c)
	}
}

func HandlePause(c *net.TCPConn, args []string) () {
	if args != nil {
//		done[args[0]]<- PAUSE
		fmt.Fprintf(c, "Rpause %s\n", args[0])
	} else {
		Rerror(c)
	}
}

func HandleCont(c *net.TCPConn, args []string) () {
	if args != nil {
//		done[args[0]]<- CONTINUE
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

		//fmt.Printf("read %#v\n", string(line))
		tok := strings.Split(string(line), " ")
		//fmt.Printf("%#v\n", tok)

		//handlers[tok[0]](tok[1:])
		h := handlers[tok[0]]
		if h != nil {
			h(c, tok[1:])
		} else {
			fmt.Fprintf(c, "Rerror\n")
		}
	}
}
