package osm

import (
	"math"
)

type Node struct {
	Id	uint
	Lat	float64 // Lat and Lon are stored in degrees
	Lon	float64
	LatRad	float64 // Radians
	LonRad	float64
}

type Way struct {
	Id	uint
	Nodes	[]uint // the IDs of the nodes in the way
	Name	string // 4th Street, East Ave, etc.
	Type	string // secondary, residential, etc.
}

func NewNode(id uint, lat, lon float64) (n *Node) {
	n = new(Node)
	n.Id = id
	n.Lat = lat
	n.Lon = lon

	return n
}

func FindWays(ways []Way, node uint) (ret []Way) {
	for _, w := range ways {
		for _, id := range w.Nodes {
			if id == node {
				ret = append(ret, w)
				break
			}
		}
	}
	return
}

// Returns the distance between the two nodes in meters
// Uses equirectangular approximation due to short distances
func GetDist(n1, n2 Node) float64 {
	const R float64 = 6371000
	x := ((math.Pi/180)*(n2.Lon - n1.Lon)) * math.Cos((math.Pi/180)*n1.Lat)
	y := (math.Pi/180)*(n2.Lat - n1.Lat)
	return R * math.Sqrt(x*x + y*y)
}
