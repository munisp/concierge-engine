package server

// Phase 35 acquisition geography: embedded reference tables for the
// airport (A1) and city-pair route (A2) SEO pages. Coordinates are
// well-known reference points (airport terminals / city centres) — stable
// public facts, used for distance estimates and coverage checks only,
// never presented as surveyed data.

type geoPoint struct {
	Code string  // IATA or slug
	Name string  // display name
	City string  // city the airport serves (airports only)
	Lat  float64 // reference point
	Lng  float64
}

// airports35: the world's major premium-travel gateways.
var airports35 = []geoPoint{
	{"AMS", "Amsterdam Airport Schiphol", "Amsterdam", 52.3105, 4.7683},
	{"DXB", "Dubai International Airport", "Dubai", 25.2532, 55.3657},
	{"AUH", "Zayed International Airport", "Abu Dhabi", 24.4330, 54.6511},
	{"LHR", "London Heathrow Airport", "London", 51.4700, -0.4543},
	{"LGW", "London Gatwick Airport", "London", 51.1537, -0.1821},
	{"LCY", "London City Airport", "London", 51.5048, 0.0495},
	{"CDG", "Paris Charles de Gaulle Airport", "Paris", 49.0097, 2.5479},
	{"ORY", "Paris Orly Airport", "Paris", 48.7233, 2.3794},
	{"LBG", "Paris-Le Bourget Airport", "Paris", 48.9694, 2.4414},
	{"FRA", "Frankfurt Airport", "Frankfurt", 50.0379, 8.5622},
	{"MUC", "Munich Airport", "Munich", 48.3537, 11.7750},
	{"BER", "Berlin Brandenburg Airport", "Berlin", 52.3667, 13.5033},
	{"ZRH", "Zurich Airport", "Zurich", 47.4581, 8.5555},
	{"GVA", "Geneva Airport", "Geneva", 46.2381, 6.1089},
	{"MXP", "Milan Malpensa Airport", "Milan", 45.6301, 8.7231},
	{"LIN", "Milan Linate Airport", "Milan", 45.4494, 9.2783},
	{"FCO", "Rome Fiumicino Airport", "Rome", 41.8003, 12.2389},
	{"MAD", "Madrid-Barajas Airport", "Madrid", 40.4983, -3.5676},
	{"BCN", "Barcelona-El Prat Airport", "Barcelona", 41.2974, 2.0833},
	{"LIS", "Lisbon Humberto Delgado Airport", "Lisbon", 38.7742, -9.1342},
	{"JFK", "John F. Kennedy International Airport", "New York", 40.6413, -73.7781},
	{"EWR", "Newark Liberty International Airport", "New York", 40.6895, -74.1745},
	{"TEB", "Teterboro Airport", "New York", 40.8501, -74.0608},
	{"LAX", "Los Angeles International Airport", "Los Angeles", 33.9416, -118.4085},
	{"MIA", "Miami International Airport", "Miami", 25.7959, -80.2870},
	{"ORD", "Chicago O'Hare International Airport", "Chicago", 41.9742, -87.9073},
	{"IAD", "Washington Dulles International Airport", "Washington", 38.9531, -77.4565},
	{"SFO", "San Francisco International Airport", "San Francisco", 37.6213, -122.3790},
	{"BOS", "Boston Logan International Airport", "Boston", 42.3656, -71.0096},
	{"YYZ", "Toronto Pearson International Airport", "Toronto", 43.6777, -79.6248},
	{"IST", "Istanbul Airport", "Istanbul", 41.2753, 28.7519},
	{"DOH", "Hamad International Airport", "Doha", 25.2731, 51.6081},
	{"RUH", "King Khalid International Airport", "Riyadh", 24.9576, 46.6988},
	{"CAI", "Cairo International Airport", "Cairo", 30.1219, 31.4056},
	{"LOS", "Murtala Muhammed International Airport", "Lagos", 6.5774, 3.3212},
	{"ABV", "Nnamdi Azikiwe International Airport", "Abuja", 9.0068, 7.2632},
	{"NBO", "Jomo Kenyatta International Airport", "Nairobi", -1.3192, 36.9278},
	{"JNB", "O. R. Tambo International Airport", "Johannesburg", -26.1367, 28.2411},
	{"CPT", "Cape Town International Airport", "Cape Town", -33.9715, 18.6021},
	{"SIN", "Singapore Changi Airport", "Singapore", 1.3644, 103.9915},
	{"HKG", "Hong Kong International Airport", "Hong Kong", 22.3080, 113.9185},
	{"NRT", "Narita International Airport", "Tokyo", 35.7720, 140.3929},
	{"HND", "Haneda Airport", "Tokyo", 35.5494, 139.7798},
	{"SYD", "Sydney Kingsford Smith Airport", "Sydney", -33.9399, 151.1753},
	{"BOM", "Chhatrapati Shivaji Maharaj International Airport", "Mumbai", 19.0896, 72.8656},
	{"DEL", "Indira Gandhi International Airport", "Delhi", 28.5562, 77.1000},
	{"GRU", "São Paulo/Guarulhos International Airport", "São Paulo", -23.4356, -46.4731},
}

func airportByIATA(code string) *geoPoint {
	for i := range airports35 {
		if airports35[i].Code == code {
			return &airports35[i]
		}
	}
	return nil
}

// cities35: city centres for the route-estimate pages (A2).
var cities35 = []geoPoint{
	{Code: "amsterdam", Name: "Amsterdam", Lat: 52.3676, Lng: 4.9041},
	{Code: "rotterdam", Name: "Rotterdam", Lat: 51.9244, Lng: 4.4777},
	{Code: "the-hague", Name: "The Hague", Lat: 52.0705, Lng: 4.3007},
	{Code: "utrecht", Name: "Utrecht", Lat: 52.0907, Lng: 5.1214},
	{Code: "eindhoven", Name: "Eindhoven", Lat: 51.4416, Lng: 5.4697},
	{Code: "brussels", Name: "Brussels", Lat: 50.8503, Lng: 4.3517},
	{Code: "antwerp", Name: "Antwerp", Lat: 51.2194, Lng: 4.4025},
	{Code: "paris", Name: "Paris", Lat: 48.8566, Lng: 2.3522},
	{Code: "london", Name: "London", Lat: 51.5074, Lng: -0.1278},
	{Code: "dubai", Name: "Dubai", Lat: 25.2048, Lng: 55.2708},
	{Code: "abu-dhabi", Name: "Abu Dhabi", Lat: 24.4539, Lng: 54.3773},
	{Code: "berlin", Name: "Berlin", Lat: 52.5200, Lng: 13.4050},
	{Code: "munich", Name: "Munich", Lat: 48.1351, Lng: 11.5820},
	{Code: "frankfurt", Name: "Frankfurt", Lat: 50.1109, Lng: 8.6821},
	{Code: "zurich", Name: "Zurich", Lat: 47.3769, Lng: 8.5417},
	{Code: "geneva", Name: "Geneva", Lat: 46.2044, Lng: 6.1432},
	{Code: "milan", Name: "Milan", Lat: 45.4642, Lng: 9.1900},
	{Code: "rome", Name: "Rome", Lat: 41.9028, Lng: 12.4964},
	{Code: "madrid", Name: "Madrid", Lat: 40.4168, Lng: -3.7038},
	{Code: "barcelona", Name: "Barcelona", Lat: 41.3874, Lng: 2.1686},
	{Code: "lisbon", Name: "Lisbon", Lat: 38.7223, Lng: -9.1393},
	{Code: "new-york", Name: "New York", Lat: 40.7128, Lng: -74.0060},
	{Code: "los-angeles", Name: "Los Angeles", Lat: 34.0522, Lng: -118.2437},
	{Code: "miami", Name: "Miami", Lat: 25.7617, Lng: -80.1918},
	{Code: "lagos", Name: "Lagos", Lat: 6.5244, Lng: 3.3792},
	{Code: "abuja", Name: "Abuja", Lat: 9.0765, Lng: 7.3986},
	{Code: "nairobi", Name: "Nairobi", Lat: -1.2921, Lng: 36.8219},
	{Code: "johannesburg", Name: "Johannesburg", Lat: -26.2041, Lng: 28.0473},
	{Code: "cape-town", Name: "Cape Town", Lat: -33.9249, Lng: 18.4241},
	{Code: "singapore", Name: "Singapore", Lat: 1.3521, Lng: 103.8198},
	{Code: "hong-kong", Name: "Hong Kong", Lat: 22.3193, Lng: 114.1694},
	{Code: "tokyo", Name: "Tokyo", Lat: 35.6762, Lng: 139.6503},
	{Code: "sydney", Name: "Sydney", Lat: -33.8688, Lng: 151.2093},
	{Code: "istanbul", Name: "Istanbul", Lat: 41.0082, Lng: 28.9784},
	{Code: "doha", Name: "Doha", Lat: 25.2854, Lng: 51.5310},
	{Code: "riyadh", Name: "Riyadh", Lat: 24.7136, Lng: 46.6753},
	{Code: "cairo", Name: "Cairo", Lat: 30.0444, Lng: 31.2357},
	{Code: "mumbai", Name: "Mumbai", Lat: 19.0760, Lng: 72.8777},
	{Code: "delhi", Name: "Delhi", Lat: 28.6139, Lng: 77.2090},
}

func cityBySlug(slug string) *geoPoint {
	for i := range cities35 {
		if cities35[i].Code == slug {
			return &cities35[i]
		}
	}
	return nil
}
