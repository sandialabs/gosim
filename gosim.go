/*
 * Wandering person simulator
 * This reads in an OpenStreetMaps file, parses the XML, and constructs
 * an internal representation of the streets represented therein.
 * It then has a person randomly walk along the streets.
 */

package main

import (
	"bufio"
	"encoding/xml"
	"fmt"
	"github.com/floren/ellipsoid"
	"io"
	"launchpad.net/mgo"
	"launchpad.net/mgo/bson"
	"log"
	"math/rand"
	"net"
	"os"
	"strings"
	"time"
)

var (
	handlers map[string]func(*net.TCPConn, []string)
	people   map[string]*Person
	session  *mgo.Session
	osm      *Osm

	randomizer *rand.Rand
	geo        ellipsoid.Ellipsoid
	newPerson  chan *Person
	command    chan Command
)

const (
	STOP = iota
	CONTINUE
	PAUSE
)

type Command struct {
	Command int
	UID     string
}

type Nd struct {
	Ref int `xml:"ref,attr"`
}

type Tag struct {
	K string `xml:"k,attr"`
	V string `xml:"v,attr"`
}

type Way struct {
	Nd   []Nd	`xml:"nd"`	
	Tag  []*Tag	`xml:"tag"`
	Node []*Node
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
	Id  int     `xml:"id,attr"`
	Lat float64 `xml:"lat,attr"`
	Lon float64 `xml:"lon,attr"`
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
	Node     []*Node	`xml:"node"`
	Way      []*Way		`xml:"way"`
	Relation []*Relation	`xml:"relation"`
}

func NewOsmXml(rd io.Reader) (o *Osm, err error) {
	err = xml.NewDecoder(rd).Decode(&o)
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
	UID     string /* Which android device is this? Should be unique.*/
	loc     Node
	dest    Node
	bearing float64
	way     *Way
	enabled bool
}

func NewPerson(uid string, n Node) (p Person) {
	return Person{UID: uid, loc: n, enabled: true}
}

func (p *Person) Move(dist float64) {
	var rdist float64 // remaining dist
	rdist, p.bearing = p.loc.Distance(p.dest)
	fmt.Printf("dist = %v, bearing = %v\n", rdist, p.bearing)
	// what happens with zero value?
	p.SetLoc(p.loc.Move(dist, p.bearing))
	// if we get within one time-period's worth of distance... assume we've made it.
	if rdist, _ = p.loc.Distance(p.dest); rdist < dist {
		p.loc = p.dest
		// Decide where to go
		p.SetWay(p.Loc().RandWay())
		p.SetDest((p.Loc().RandWay()).RandNode())
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
	UID string
	Lat float64
	Lon float64
}

func UpdatePeople() {
	if session != nil {
		c := session.DB("megadroid").C("phones")
		for _, p := range people {
			fmt.Printf("updating %v\n", p)
			_, err := c.Upsert(bson.M{"uid": p.UID}, &DBPerson{p.UID, p.Loc().Lat, p.Loc().Lon})
			if err != nil {
				panic(err)
			}
		}
	}
}

func init() {
	handlers = map[string]func(*net.TCPConn, []string){"Tpos": HandlePos,
		"Tstart":    HandleStart,
		"Tstop":     HandleStop,
		"Tpause":    HandlePause,
		"Tcontinue": HandleCont}

	randomizer = rand.New(rand.NewSource(time.Now().UnixNano()))
	geo = ellipsoid.Init("WGS84", ellipsoid.Degrees, ellipsoid.Meter, ellipsoid.Longitude_is_symmetric, ellipsoid.Bearing_is_symmetric)

	newPerson = make(chan *Person)
	command = make(chan Command)
	people = make(map[string]*Person)
	ticker := time.NewTicker(1e9)

	go func() {
		for {
			select {
			case p := <-newPerson:
				fmt.Printf("read %v off of the channel\n", p)
				people[p.UID] = p
				fmt.Printf("got a new person %v\n", p)
			case c := <-command:
				switch c.Command {
				case PAUSE:
					people[c.UID].enabled = false
				case CONTINUE:
					people[c.UID].enabled = true
				case STOP:
					delete(people, c.UID)
				}
			case <-ticker.C:
				for _, p := range people {
					// average person (male) walks 1.56464 m/s
					if p.enabled == true {
						p.Move(1.56464)
					}
				}
				UpdatePeople()
			}
		}
	}()
}

func main() {
	var err error
	f, err := os.Open(os.Args[1])
	defer f.Close()
	if err != nil {
		log.Fatal(err)
	}

	osm, err = NewOsmXml(f)

	session, err = mgo.Dial("localhost")
	if err != nil {
		panic(err)
	}
	defer session.Close()
	c := session.DB("megadroid").C("phones")
	c.DropCollection()

	rand.Seed(time.Now().UnixNano() % 1e9)

	addr, _ := net.ResolveTCPAddr("tcp", ":4001")
	l, err := net.ListenTCP("tcp", addr)
	if err != nil {
		panic(err)
	}

	for {
		c, _ := l.AcceptTCP()
		fmt.Printf("accepted call from %v\n", c.RemoteAddr())
		go Handler(c)
	}
}

// Functions after this point are used to handle the network protocol.

func readline(b *bufio.Reader) (p []byte, err error) {
	if p, err = b.ReadSlice('\n'); err != nil {
		return nil, err
	}
	var i int
	for i = len(p); i > 0; i-- {
		if c := p[i-1]; c != '\r' && c != '\n' {
			break
		}
	}
	return p[0:i], nil
}

func HandleStart(c *net.TCPConn, args []string) {
	uid := args[0]

	fmt.Printf("Creating new wanderer with ID = %v\n", uid)
	p := NewPerson(uid, osm.RandNode())
	// Decide where to go
	p.SetWay(p.Loc().RandWay())
	p.SetDest((p.Loc().RandWay()).RandNode())
	newPerson <- &p
	fmt.Fprintf(c, "Rstart %s\n", uid)
}

func Rerror(c *net.TCPConn) {
	fmt.Fprintf(c, "Rerror\n")
}

// We need to talk to the database, unfortunately, because if we scale this up to multiple machines,
// it's quite likely that the UID you're asking about will be simulated on a different computer
func HandlePos(c *net.TCPConn, args []string) {
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

func HandleStop(c *net.TCPConn, args []string) {
	if args != nil {
		uid := args[0]
		command <- Command{STOP, uid}
		fmt.Fprintf(c, "Rstop %s\n", uid)
	} else {
		Rerror(c)
	}
}

func HandlePause(c *net.TCPConn, args []string) {
	if args != nil {
		uid := args[0]
		command <- Command{PAUSE, uid}
		fmt.Fprintf(c, "Rpause %s\n", args[0])
	} else {
		Rerror(c)
	}
}

func HandleCont(c *net.TCPConn, args []string) {
	if args != nil {
		uid := args[0]
		command <- Command{CONTINUE, uid}
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
		if err != nil {
			fmt.Print("exiting\n")
			return
		}

		tok := strings.Split(string(line), " ")

		h := handlers[tok[0]]
		if h != nil {
			h(c, tok[1:])
		} else {
			fmt.Fprintf(c, "Rerror\n")
		}
	}
}
