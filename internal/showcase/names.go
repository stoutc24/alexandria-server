package showcase

import "fmt"

// Everything in this file is invented for the showcase: people, titles,
// blurbs. None of it is anyone's work, and the combinations were chosen
// to avoid known titles. The hero entries (the ones that lead a shelf or
// sit in a screenshot's foreground) are written out; the long tail is
// composed from the word banks so a library looks full.

type person struct {
	Name string
	Bio  string
}

var authors = []person{
	{"Marisol Vane", "Writes coastal mysteries where the weather is a suspect. Lives on a tidal island with a very old dog."},
	{"Ezra Quill", "Former cartographer turned novelist. His winters are long and his sentences are longer."},
	{"Odette Marsh", "Naturalist and essayist; her field guides read like memoirs."},
	{"Tobias Renshaw", "Writes about engineers, rivers and the people who stand between them."},
	{"Imani Okafor-Lind", "Historian of small towns and the big ideas that pass through them."},
	{"Petra Halloway", "Thrillers set in observatories, lighthouses and other places with one road in."},
	{"Caius Mendel", "Speculative fiction about work, machines and the first morning after."},
	{"Rosalind Achebe-Finch", "Family sagas across three continents and four kitchens."},
	{"Hollis Grey", "Quiet horror. Nothing jumps; everything waits."},
	{"Yusuf Adebayo", "Comedies of manners for people who hate manners."},
	{"Lena Marchetti", "Travel writing from places that are no longer on the map."},
	{"Soren Blackwood", "Epic fantasy with a tax code."},
}

var narrators = []string{
	"Delphine Arquette", "Marcus Oyelaran", "Greta Lindqvist", "Jonah Pike",
	"Amara Bellweather", "Teodor Vasquez", "Priya Ramaswamy", "Callum Reid",
}

type bookSeed struct {
	Title     string
	Author    int // index into authors
	Narrator  int
	Series    string
	Sequence  string
	Genres    []string
	Year      string
	Hours     float64
	Blurb     string
	Ebook     bool
	Progress  float64 // 0 = unstarted, 1 = finished, else in progress
	AddedDays int     // days ago
	Publisher string
}

var books = []bookSeed{
	{"The Lighthouse at Sable Point", 0, 0, "Sable Point", "1", []string{"Mystery", "Fiction"}, "2021", 11.4, "A keeper's log stops mid-sentence in 1954. Seventy years on, Nell Ashworth inherits the lamp, the log, and the question nobody on the island will answer.", false, 0.62, 40, "Tidewater Press"},
	{"Salt and Signal", 0, 0, "Sable Point", "2", []string{"Mystery", "Fiction"}, "2022", 12.1, "The ferry stops running in November. The second Sable Point mystery starts the morning after.", false, 0, 30, "Tidewater Press"},
	{"The Last Low Water", 0, 0, "Sable Point", "3", []string{"Mystery", "Fiction"}, "2024", 12.8, "Nell knows every tide table on the coast. She did not expect to need one for a funeral.", false, 0, 6, "Tidewater Press"},
	{"Nine Winters in Tallow", 1, 3, "", "", []string{"Literary Fiction"}, "2019", 15.2, "A surveyor returns to the valley he mapped as a young man and finds the river has disagreed with him.", false, 1, 200, "Meridian House"},
	{"A Field Guide to Vanished Rivers", 2, 2, "", "", []string{"Nature", "Essays"}, "2020", 7.3, "Twelve walks along water that used to be there, and what grows in its place.", true, 0.18, 12, "Lantern & Co."},
	{"The Hollow Mile", 2, 2, "", "", []string{"Nature", "Memoir"}, "2023", 8.9, "One mile of hedgerow, one year, one notebook.", false, 0, 90, "Lantern & Co."},
	{"Bridgework", 3, 1, "", "", []string{"Historical Fiction"}, "2018", 14.6, "Three generations build the same bridge; only one of them finishes it.", false, 0.91, 150, "Meridian House"},
	{"The Weir Keepers", 3, 1, "The Weir", "1", []string{"Historical Fiction", "Family Saga"}, "2021", 16.0, "The first of the Weir novels. A mill town, a flood, and the family that decides who gets warned.", false, 0, 60, "Meridian House"},
	{"High Water Mark", 3, 1, "The Weir", "2", []string{"Historical Fiction", "Family Saga"}, "2023", 15.5, "The second Weir novel. The town rebuilds; the river takes notes.", false, 0, 20, "Meridian House"},
	{"Small Town, Big Sky", 4, 4, "", "", []string{"History", "Nonfiction"}, "2017", 9.8, "How a county fair, a radio tower and a disputed well shaped a century of one town.", false, 1, 300, "Northline Books"},
	{"The Committee of Tuesdays", 4, 4, "", "", []string{"History", "Nonfiction"}, "2022", 10.4, "Six people met every Tuesday for forty years. This is what they decided.", false, 0.35, 25, "Northline Books"},
	{"Zenith Station", 5, 6, "Zenith", "1", []string{"Thriller"}, "2020", 10.9, "An observatory at the end of the only road. A night with no stars. A door that was locked from the outside.", false, 0, 45, "Blackline"},
	{"The Dark Interval", 5, 6, "Zenith", "2", []string{"Thriller"}, "2022", 11.3, "Sequel to Zenith Station. The telescope is pointed at the ground.", false, 0.07, 14, "Blackline"},
	{"First Light Protocol", 5, 6, "Zenith", "3", []string{"Thriller"}, "2024", 11.7, "The Zenith trilogy ends where the road does.", false, 0, 3, "Blackline"},
	{"The Morning After the Machines", 6, 5, "", "", []string{"Science Fiction"}, "2021", 13.2, "The factories finish everything overnight. Nobody told the day shift.", false, 0, 70, "Orbit Lane"},
	{"Idle Hands", 6, 5, "", "", []string{"Science Fiction", "Short Stories"}, "2023", 6.4, "Eleven stories about work that no longer needs doing.", false, 1, 110, "Orbit Lane"},
	{"Four Kitchens", 7, 7, "", "", []string{"Family Saga", "Literary Fiction"}, "2019", 17.8, "Lagos, Lisbon, Louisville, Lund. One recipe, four kitchens, a hundred years.", false, 0.48, 55, "Meridian House"},
	{"The Sunday Table", 7, 7, "", "", []string{"Family Saga"}, "2022", 15.1, "What happens when the one who always cooked stops cooking.", false, 0, 35, "Meridian House"},
	{"The House That Listens", 8, 0, "", "", []string{"Horror"}, "2020", 9.2, "The walls are not thin. They are attentive.", false, 0, 80, "Blackline"},
	{"Still Water, Deep Rooms", 8, 0, "", "", []string{"Horror"}, "2023", 9.9, "A hotel on a lake. Every room has a view of the same boat.", false, 0.23, 9, "Blackline"},
	{"Please Remain Seated", 9, 3, "", "", []string{"Comedy", "Fiction"}, "2021", 8.1, "A wedding, a will, and a seating plan that constitutes an act of war.", false, 1, 130, "Lantern & Co."},
	{"Reply All", 9, 3, "", "", []string{"Comedy", "Fiction"}, "2024", 7.7, "One email. Four hundred recipients. No way back.", false, 0, 2, "Lantern & Co."},
	{"Roads That Were", 10, 2, "", "", []string{"Travel", "Nonfiction"}, "2018", 8.6, "Walking the routes that atlases stopped printing.", true, 0, 95, "Northline Books"},
	{"The Ferry Towns", 10, 2, "", "", []string{"Travel", "Nonfiction"}, "2022", 9.4, "Twenty crossings, twenty towns, and the people who wait on both banks.", false, 0.55, 18, "Northline Books"},
	{"The Ledger of Thornwall", 11, 1, "Thornwall", "1", []string{"Fantasy"}, "2020", 21.3, "A kingdom with excellent accountants and a dragon problem that is also a receipts problem.", false, 0.3, 50, "Orbit Lane"},
	{"The Audit of Kings", 11, 1, "Thornwall", "2", []string{"Fantasy"}, "2022", 22.0, "Book two of Thornwall. The crown is fine; the paperwork is not.", false, 0, 28, "Orbit Lane"},
	{"Crown and Quarter", 11, 1, "Thornwall", "3", []string{"Fantasy"}, "2024", 23.1, "The Thornwall trilogy concludes with a siege and a surprisingly fair settlement.", false, 0, 1, "Orbit Lane"},
	{"Lantern Hours", 1, 3, "", "", []string{"Literary Fiction"}, "2022", 10.6, "A night porter's year, told one shift at a time.", true, 0, 65, "Meridian House"},
	{"Weather for Beginners", 2, 2, "", "", []string{"Nature", "Science"}, "2021", 6.9, "How to read a sky without an app.", false, 1, 170, "Lantern & Co."},
	{"The Quiet Belt", 6, 5, "", "", []string{"Science Fiction"}, "2019", 12.4, "A ring of silent satellites, and the one operator who notices.", false, 0, 140, "Orbit Lane"},
	{"Thirteen Bells", 5, 6, "", "", []string{"Thriller"}, "2018", 10.2, "A cathedral town, one ringer short.", false, 1, 220, "Blackline"},
	{"Harvest of Small Hours", 7, 7, "", "", []string{"Literary Fiction"}, "2017", 13.0, "Night shifts, early markets, and a bakery that holds a family together.", false, 0, 260, "Meridian House"},
	{"The Cartographer's Daughter", 1, 3, "", "", []string{"Adventure", "Fiction"}, "2016", 14.1, "She inherits the maps. The blank parts are not blank.", false, 0, 330, "Meridian House"},
	{"A Short History of Fences", 4, 4, "", "", []string{"History"}, "2020", 7.5, "Who put them up, who took them down, and why they keep coming back.", true, 0, 100, "Northline Books"},
	{"Good Neighbours", 9, 3, "", "", []string{"Comedy"}, "2019", 8.3, "A street party planned by committee. Nobody survives the agenda.", false, 0.8, 7, "Lantern & Co."},
	{"Low Tide at Ferris Cove", 0, 0, "", "", []string{"Mystery"}, "2018", 10.7, "Before Sable Point there was Ferris Cove, and a boat that came back without its crew.", false, 0, 180, "Tidewater Press"},
}

type artistSeed struct {
	Name  string
	Genre string
	Bio   string
}

var artists = []artistSeed{
	{"The Harbor Lights", "Indie Folk", "Four friends, two boats and a barn full of instruments."},
	{"Juniper Vale", "Dream Pop", "Night songs for day people."},
	{"Cassius & The Ledger", "Soul", "Horns, handclaps and a rhythm section that files its own paperwork."},
	{"Marlowe Finch", "Singer-Songwriter", "One guitar, one voice, a lot of weather."},
	{"North Pier Collective", "Jazz", "A rotating cast of eleven; never the same eleven twice."},
	{"Elowen Sky", "Electronic", "Modular synths recorded in a greenhouse."},
	{"The Tideglass", "Alternative", "Loud songs about quiet places."},
	{"Rafferty June", "Country", "Songs from the back roads between two small towns."},
	{"Signal Hill Brass", "Brass Band", "Seven players, one hill, every Sunday."},
	{"Ivo Castellane", "Classical Guitar", "Nocturnes and studies, recorded in one take."},
	{"Paper Lanterns", "Indie Rock", "Bright, brief and a little bit sad."},
	{"The Gilded Hour", "Chamber Pop", "Strings, a piano and three-part harmonies."},
	{"Sable & Ash", "Blues", "Two voices, one porch."},
	{"Wren Halliday", "Ambient", "Long pieces for long evenings."},
}

type albumSeed struct {
	Title  string
	Artist int
	Year   int
	Tracks []string
	Played float64 // fraction of the album listened to lately (shelves)
	Added  int     // days ago
}

var albums = []albumSeed{
	{"Copperline", 0, 2024, []string{"Copperline", "Ferry Lights", "Morning on the Slip", "Low Tide Waltz", "Salt in the Lines", "Harbor Bell", "North Wind Lullaby", "Last Boat Home"}, 0.6, 10},
	{"Barn Sessions", 0, 2021, []string{"Rafters", "Haybale Two-Step", "Kettle Song", "The Long Table", "Lantern Walk", "Field Edge", "Barn Owl", "Dawn Chorus", "Gate Latch"}, 0, 400},
	{"Orbit of Small Hours", 1, 2023, []string{"Small Hours", "Streetlight Static", "Velvet Signal", "Night Bus", "Glass Moon", "Sleepwalk Radio", "Quiet Satellite", "Blue Hour", "Soft Landing"}, 0.3, 45},
	{"Daylight Saving", 1, 2020, []string{"Hour Hand", "Later Than You Think", "Window Seat", "Pale Static", "Second Sun", "Sunday Drift", "Clockwork Kiss", "Afterglow"}, 0, 520},
	{"Receipts", 2, 2022, []string{"Carry the One", "Overtime", "Dividend", "Paid in Full", "Ledger Line", "Balance", "Interest", "Bonus Round", "Closing Time"}, 1, 300},
	{"Live at the Mill", 2, 2024, []string{"Intro at the Mill", "Carry the One (Live)", "Grindstone", "Water Wheel", "Overtime (Live)", "Encore: Dividend"}, 0, 20},
	{"Low Pressure", 3, 2022, []string{"Low Pressure", "Rain on the Line", "Tin Roof", "Cold Front", "Open Window", "Barometer", "Clearing", "Red Sky"}, 0.8, 120},
	{"Field Recordings", 3, 2019, []string{"Fence Post", "Gravel", "Wire Song", "Hawk Circle", "Dust Road", "Evening Train", "Porch Light"}, 0, 600},
	{"Pier Sessions Vol. 1", 4, 2021, []string{"Gull Theme", "Breakwater", "Three for the Tide", "Rope and Rust", "Fog Horn Blues", "Night Pier", "Coda for Eleven"}, 0.5, 200},
	{"Pier Sessions Vol. 2", 4, 2023, []string{"Slack Water", "Harbor Master", "Eleven Again", "Shanty for Brass", "Low Light", "Anchor Chain", "Last Set"}, 0, 70},
	{"Greenhouse", 5, 2024, []string{"Germinate", "Glass Roof", "Humidity", "Fern Logic", "Night Bloom", "Drip Line", "Photosynthesis", "Harvest Loop", "Compost", "Seedbank"}, 0.2, 15},
	{"Loud Songs About Quiet Places", 6, 2023, []string{"Library", "Chapel", "Snowfield", "Museum Hours", "Deep End", "Reading Room", "Night Shift", "Observatory"}, 0, 150},
	{"Back Roads", 7, 2022, []string{"Two Towns Over", "Gravel Hymn", "Diner Neon", "Mile Marker", "Hay Season", "Truck Radio", "Sunday Fence", "Back Roads"}, 0.9, 90},
	{"Every Sunday", 8, 2020, []string{"Procession", "Hill Top", "Brass Bells", "Slow March", "Fanfare for Seven", "Benediction"}, 0, 350},
	{"Nocturnes & Studies", 9, 2021, []string{"Nocturne No. 1", "Study in E", "Nocturne No. 2", "Study for Left Hand", "Nocturne No. 3", "Barcarolle", "Study in Octaves", "Nocturne No. 4"}, 0.4, 250},
	{"Bright, Brief", 10, 2024, []string{"Matchstick", "Sparkler", "Two Minutes", "Paper Boat", "Lantern Festival", "Quick Goodbye", "Streetlight", "Short Summer"}, 0, 30},
	{"Three Part", 11, 2022, []string{"Overture", "Harmony Line", "Piano Room", "Cello Weather", "Quartet for Three", "Gilded", "The Hour", "Reprise"}, 0.1, 180},
	{"Porch", 12, 2023, []string{"Porch", "Screen Door", "Rocking Chair Blues", "Lemonade", "Heat Lightning", "Cicadas", "Last Light", "Goodnight, Ash"}, 0, 110},
	{"Long Evenings", 13, 2021, []string{"Dusk I", "Dusk II", "Lamp", "Still Room", "Window Rain", "Dusk III"}, 0.7, 60},
	{"Winter Drift", 13, 2024, []string{"First Frost", "Snow Line", "Ice Field", "Thaw", "Drift"}, 0, 5},
	{"Harbor Lights EP", 0, 2019, []string{"Harbor Lights", "Slip Road", "Night Ferry", "Tidal"}, 0, 700},
	{"Second Pressing", 2, 2020, []string{"B-Side Boogie", "Spare Change", "Loose Ends", "Count It Twice", "Fine Print"}, 0, 450},
}

// castMember is one credited performer: the actor and the part played
// ("Self" in a documentary). Names are short on purpose: the TV's cast
// cards are 170 pt wide and truncate a name past ~11 characters.
type castMember struct {
	Name string
	Role string
}

type movieSeed struct {
	Title    string
	Year     int
	Minutes  int
	Rating   string
	Genres   []string
	Tagline  string
	Summary  string
	Director string
	Cast     []castMember
	Score    float64
	Progress float64 // 0 unstarted, 1 watched, else partial
	Added    int     // days ago
	UHD      bool
}

var movies = []movieSeed{
	{"The Cartographer's Daughter", 2023, 118, "PG-13", []string{"Adventure", "Drama"}, "The blank parts are not blank.", "When Ines inherits her father's unfinished atlas, the missing coastline turns out to be missing on purpose.", "Halle Brennan", []castMember{{"Ines Okoro", "Ines"}, {"Teo Varga", "Aurel"}, {"Mira Solen", "Tove"}}, 7.8, 0.42, 20, true},
	{"Night Ferry", 2022, 104, "R", []string{"Thriller"}, "Last crossing. No return.", "Six passengers, one ferry, and a storm that closes the harbor behind them.", "Rowan Pike", []castMember{{"Cal Reid", "The Captain"}, {"Amara Bell", "Ruth"}}, 7.1, 1, 200, false},
	{"Harbor Bell", 2021, 96, "PG", []string{"Family", "Comedy"}, "A town, a bell, and a very small thief.", "The harbor bell goes missing the week before the regatta. Nine-year-old Pip has a plan and a wagon.", "Juno Marsh", []castMember{{"Pip Dorsey", "Pip"}, {"Nell Fane", "Harbormaster"}, {"Bram Ives", "Grandpa Ike"}}, 6.9, 0, 400, false},
	{"Zenith Station", 2024, 127, "PG-13", []string{"Thriller", "Science Fiction"}, "A night with no stars.", "At an observatory at the end of the only road, the night sky goes dark and the door is locked from the outside.", "Petra Halloway", []castMember{{"Kofi Adair", "Dr. Osei"}, {"Greta Lune", "Lena"}, {"Jonah Pike", "Night Tech"}}, 8.2, 0.15, 8, true},
	{"The Weir Keepers", 2020, 141, "PG-13", []string{"Drama", "Historical"}, "Who gets warned.", "A mill town, a flood, and the family that decides who hears first.", "Imani Okafor-Lind", []castMember{{"Priya Sen", "Agnes Weir"}, {"Otto Grey", "Thomas Weir"}}, 7.6, 1, 600, false},
	{"Reply All", 2024, 92, "PG-13", []string{"Comedy"}, "One email. Four hundred recipients.", "An office comedy about the message that could not be unsent.", "Yusuf Adebayo", []castMember{{"Teo Varga", "Dev"}, {"Amara Bell", "Marnie"}}, 7.0, 0, 3, false},
	{"Still Water", 2023, 109, "R", []string{"Horror"}, "Every room has a view of the same boat.", "A lakeside hotel reopens for one season. The guests keep seeing the same rowboat.", "Hollis Grey", []castMember{{"Greta Lune", "Hester"}, {"Cal Reid", "Night Clerk"}}, 6.8, 0, 60, true},
	{"Copperline", 2022, 88, "PG", []string{"Documentary", "Music"}, "Four friends, two boats, one record.", "A year with The Harbor Lights as they record an album in a barn by the water.", "Lena Marchetti", []castMember{{"Ada Lowe", "Self"}, {"Finn Rook", "Self"}}, 7.9, 0.7, 45, false},
	{"The Ledger of Thornwall", 2021, 152, "PG-13", []string{"Fantasy", "Adventure"}, "A kingdom with excellent accountants.", "A dragon problem that is also a receipts problem, and the clerk who has to solve both.", "Soren Blackwood", []castMember{{"Jonah Pike", "Aldous"}, {"Priya Sen", "Queen Maren"}, {"Kofi Adair", "The Dragon"}}, 7.4, 0, 150, true},
	{"Four Kitchens", 2019, 133, "PG-13", []string{"Drama"}, "One recipe. A hundred years.", "Lagos, Lisbon, Louisville, Lund: a family follows a recipe across four kitchens and a century.", "Rosalind Achebe-Finch", []castMember{{"Amara Bell", "Folake"}, {"Nell Fane", "Leonor"}}, 8.0, 1, 700, false},
	{"Low Pressure", 2023, 101, "PG-13", []string{"Romance", "Drama"}, "Rain on the line.", "A weather forecaster and a lighthouse keeper argue about the same storm from opposite ends of a phone call.", "Marlowe Finch", []castMember{{"Cal Reid", "Rowan"}, {"Mira Solen", "Isla"}}, 7.2, 0.3, 30, false},
	{"Back Roads", 2022, 97, "PG", []string{"Comedy", "Drama"}, "Two towns over.", "A road trip between two small towns that takes all summer.", "Rafferty June", []castMember{{"Teo Varga", "Hank"}, {"Pip Dorsey", "Dot"}}, 6.7, 0, 250, false},
	{"Signal Hill", 2020, 112, "PG", []string{"Drama", "Music"}, "Seven players, one hill.", "A brass band faces its last Sunday.", "Juno Marsh", []castMember{{"Otto Grey", "Bandmaster"}, {"Greta Lune", "Win"}}, 7.3, 0, 500, false},
	{"The Quiet Belt", 2024, 124, "PG-13", []string{"Science Fiction"}, "A ring of silent satellites.", "One operator notices the silence and cannot make anyone else hear it.", "Caius Mendel", []castMember{{"Kofi Adair", "Operator Vos"}, {"Priya Sen", "Dr. Imre"}}, 7.7, 0, 12, true},
	{"Please Remain Seated", 2021, 99, "PG-13", []string{"Comedy"}, "A seating plan is an act of war.", "A wedding, a will, and a table arrangement nobody survives.", "Yusuf Adebayo", []castMember{{"Lise Hart", "Clem"}, {"Jonah Pike", "Uncle Ned"}}, 6.9, 1, 350, false},
	{"Greenhouse", 2023, 84, "G", []string{"Documentary"}, "Recorded among the ferns.", "A musician builds an album inside a greenhouse over one growing season.", "Lena Marchetti", []castMember{{"Elowen Sky", "Self"}}, 7.5, 0, 80, false},
	{"Thirteen Bells", 2019, 106, "R", []string{"Thriller", "Mystery"}, "One ringer short.", "A cathedral town, a missing bell-ringer, and a peal that should not be possible.", "Petra Halloway", []castMember{{"Cal Reid", "Canon Hale"}, {"Amara Bell", "Bea Thorne"}}, 7.0, 0, 800, false},
	{"Lantern Hours", 2022, 95, "PG-13", []string{"Drama"}, "One shift at a time.", "A night porter's year in a grand old hotel.", "Ezra Quill", []castMember{{"Teo Varga", "Night Porter"}}, 7.1, 0.55, 90, false},
	{"Idle Hands", 2024, 110, "PG-13", []string{"Science Fiction", "Anthology"}, "Work that no longer needs doing.", "Four stories about the first morning after the machines finished everything.", "Caius Mendel", []castMember{{"Greta Lune", "Mae"}, {"Kofi Adair", "Foreman"}, {"Pip Dorsey", "The Boy"}}, 7.3, 0, 25, true},
	{"The Sunday Table", 2021, 121, "PG", []string{"Drama", "Family"}, "When the cook stops cooking.", "A family learns the recipes it never wrote down.", "Rosalind Achebe-Finch", []castMember{{"Nell Fane", "Gran"}, {"Otto Grey", "Walt"}}, 7.6, 0, 420, false},
	{"Good Neighbours", 2020, 93, "PG-13", []string{"Comedy"}, "Planned by committee.", "A street party with an agenda nobody survives.", "Yusuf Adebayo", []castMember{{"Amara Bell", "Hazel"}, {"Jonah Pike", "Mr. Pruitt"}}, 6.6, 0, 650, false},
	{"Winter Drift", 2024, 102, "PG", []string{"Drama"}, "First frost to thaw.", "One valley, one winter, four households that stop speaking and start again.", "Wren Halliday", []castMember{{"Mira Solen", "Ingrid"}, {"Cal Reid", "Aksel"}}, 7.4, 0, 2, true},
	{"The Hollow Mile", 2023, 79, "G", []string{"Documentary", "Nature"}, "One mile. One year.", "A naturalist walks the same hedgerow every week for a year.", "Odette Marsh", []castMember{{"Bram Ives", "Narrator"}}, 7.8, 0, 110, false},
	{"Bridgework", 2018, 138, "PG-13", []string{"Drama", "Historical"}, "Only one of them finishes it.", "Three generations build the same bridge.", "Tobias Renshaw", []castMember{{"Otto Grey", "Elias"}, {"Priya Sen", "Mags"}}, 7.5, 1, 900, false},
}

type showSeed struct {
	Title    string
	Year     int
	Rating   string
	Genres   []string
	Summary  string
	Seasons  [][]string // episode titles per season
	Minutes  int
	WatchedS int // seasons fully watched
	WatchedE int // episodes watched into the next season (next up)
	Added    int
}

var shows = []showSeed{
	{"Low Tide at Ferris Cove", 2022, "TV-14", []string{"Mystery", "Drama"}, "A boat comes back without its crew. The town of Ferris Cove has a harbormaster, a journalist and a very long memory.", [][]string{
		{"The Empty Boat", "Slack Water", "What the Harbormaster Saw", "Spring Tide", "The Ledger", "Nets", "Night Crossing", "Low Tide"},
		{"High Water", "The Second Boat", "Fog", "The Inquest", "Rope", "Breakwater", "The Last Crossing", "Ferris Cove"},
	}, 48, 1, 3, 15},
	{"The Committee of Tuesdays", 2023, "TV-PG", []string{"Comedy"}, "Six people, one meeting room, forty years of minutes. A comedy about the small decisions that run a town.", [][]string{
		{"Any Other Business", "Item Four", "The Minutes", "Quorum", "Point of Order", "Carried", "Tabled", "Adjourned"},
	}, 27, 0, 5, 40},
	{"Zenith", 2024, "TV-14", []string{"Thriller", "Science Fiction"}, "The observatory series. Every episode is one night, and the sky is never quite right.", [][]string{
		{"First Light", "Dark Interval", "Parallax", "Occultation", "Transit", "Zenith"},
	}, 52, 0, 2, 6},
	{"Four Kitchens", 2021, "TV-PG", []string{"Drama"}, "Four cities, one family recipe, a century of Sundays.", [][]string{
		{"Lagos", "Lisbon", "Louisville", "Lund", "The Recipe", "Sunday"},
		{"Second Helpings", "The Letter", "Market Day", "Salt", "Heat", "Family Table"},
		{"Return", "The Cookbook", "Four Doors", "One Kitchen", "Last Course", "Dessert"},
	}, 55, 2, 1, 300},
	{"Back Roads", 2020, "TV-G", []string{"Travel", "Documentary"}, "Two towns over, one road at a time. A travel series about the places between places.", [][]string{
		{"Mile Marker One", "The Diner", "Hay Season", "Gravel", "The Fair", "Sunday Fence", "Truck Radio", "Home"},
		{"The Long Way", "Ferry Towns", "Roads That Were", "The Crossing", "Night Drive", "Back Roads"},
	}, 24, 1, 0, 500},
	{"Signals", 2023, "TV-PG", []string{"Science", "Documentary"}, "A series about the quiet technologies that hold the day together: clocks, tides, bells and satellites.", [][]string{
		{"Clocks", "Tides", "Bells", "Satellites", "Radio", "Rivers"},
	}, 45, 0, 0, 120},
	{"The Gilded Hour", 2024, "TV-14", []string{"Drama", "Music"}, "A chamber pop group, a touring year, and the three-part harmony that holds them together.", [][]string{
		{"Overture", "Rehearsal", "First Night", "The Van", "Harmony Line", "Reprise"},
	}, 50, 0, 4, 9},
	{"Weather for Beginners", 2022, "TV-G", []string{"Kids", "Educational"}, "How to read a sky without an app, for people who are seven.", [][]string{
		{"Clouds", "Wind", "Rain", "Frost", "Fog", "Rainbows", "Thunder", "Sunsets"},
		{"Snow", "Hail", "Dew", "Mist", "Breeze", "Heatwave", "Stars", "Seasons"},
	}, 12, 1, 6, 220},
}

// showCast: the credited leads of each series (Plex Role, Jellyfin People).
var showCast = map[string][]castMember{
	"Low Tide at Ferris Cove":   {{"Lise Hart", "Harbormaster"}, {"Ravi Cole", "Journalist"}},
	"The Committee of Tuesdays": {{"June Tam", "Chair"}, {"Bram Ives", "Secretary"}},
	"Zenith":                    {{"Mira Solen", "Dr. Hale"}, {"Otto Grey", "Night Tech"}},
	"Four Kitchens":             {{"Amara Bell", "Folake"}, {"Nell Fane", "Leonor"}},
	"Back Roads":                {{"Teo Varga", "Host"}},
	"Signals":                   {{"Priya Sen", "Host"}},
	"The Gilded Hour":           {{"Ines Okoro", "Clara"}, {"Jonah Pike", "Felix"}},
	"Weather for Beginners":     {{"June Tam", "Host"}},
}

// episodeSummaries: one line per episode, season by season, in the order
// of showSeed.Seasons.
var episodeSummaries = map[string][][]string{
	"Low Tide at Ferris Cove": {
		{
			"The Mary Dunn drifts into harbor at dawn with the engine warm and nobody aboard.",
			"The harbormaster times the tides and finds the boat could not have drifted in on its own.",
			"An old logbook entry puts a second vessel in the cove on the night the crew vanished.",
			"The highest tide in years uncovers a mooring nobody admits to owning.",
			"The journalist finds that the cannery's books balance perfectly, which is the problem.",
			"A torn net, a missing knife, and a fisherman who suddenly wants to talk.",
			"Someone takes a boat out in the fog, and the whole town pretends not to hear the engine.",
			"At the lowest tide of the year, the cove gives back something it has been keeping.",
		},
		{
			"A year on, a storm surge floods the harbor and a second boat breaks its moorings.",
			"The new wreck carries a name the harbormaster has not heard spoken in thirty years.",
			"Three days of fog close the cove, and the town turns on the journalist.",
			"The inquest reopens, and everyone's account of that night changes slightly.",
			"A length of rope, tied with a knot only one family in Ferris Cove still uses.",
			"Repairs to the breakwater stop after an anonymous letter and a very large donation.",
			"The ferry makes its last run of the season with every suspect aboard.",
			"The harbormaster, the journalist and the town finally say out loud what happened.",
		},
	},
	"The Committee of Tuesdays": {{
		"A new member asks why the agenda has carried the same item for six years.",
		"The committee debates a bench. The bench has already been ordered. Nobody knows by whom.",
		"The minutes from 1987 go missing, and the town's entire parking policy rests on them.",
		"Five members are needed to vote. Four turn up. The fifth is a matter of interpretation.",
		"A procedural dispute about biscuits escalates into a constitutional crisis.",
		"The motion passes. Now somebody has to tell the mayor.",
		"The committee postpones a decision so well that it postpones itself.",
		"Forty years of Tuesdays end with one last vote, and it is not unanimous.",
	}},
	"Zenith": {{
		"A new night technician arrives at the observatory and finds tonight's log already filled in.",
		"Every star in the eastern sky goes out for eleven minutes. Only one camera was recording.",
		"Two telescopes on the same mountain disagree about where the moon is.",
		"Something passes in front of a distant star, then passes again exactly an hour later.",
		"The road down the mountain closes for the night, and a car comes up it anyway.",
		"The crew points every instrument straight up and waits for whatever is waiting for them.",
	}},
	"Four Kitchens": {
		{
			"Lagos, 1921: a young cook writes down her mother's stew for the first time, almost right.",
			"Her daughter carries the recipe to Lisbon, where the market has never heard of half of it.",
			"A Louisville diner kitchen in 1958 adds the ingredient the family will argue about for decades.",
			"Winter in Lund. A grandson cooks the stew for strangers and finally understands it.",
			"Four handwritten versions of the same recipe turn up in one shoebox.",
			"The whole family gathers for the first time, and everyone brings their own version.",
		},
		{
			"A cookbook deal arrives, and so does the question of whose name goes on the cover.",
			"A letter from 1934 explains the missing ingredient and starts a new argument.",
			"Two cousins visit the same market decades apart and buy the same tomatoes.",
			"A bad batch, a famous critic and a very honest review.",
			"The restaurant kitchen catches fire on the busiest night of the year.",
			"They rebuild the kitchen around the one table that refused to burn.",
		},
		{
			"The youngest goes back to Lagos to find the kitchen where it all started.",
			"The book is finished. The family reads it and recognises nobody.",
			"Four kitchens, four doors, and a knock at each in the same week.",
			"For the first time in a century, everyone cooks in the same room.",
			"A long dinner, a short speech, and the stew served exactly as written.",
			"Nobody ever wrote down the dessert, so they make it up together.",
		},
	},
	"Back Roads": {
		{
			"The trip starts at a mile marker nobody has repainted since 1970.",
			"A diner that has opened every day for sixty years, and the pie that keeps it open.",
			"Three generations bring the hay in before the weather turns.",
			"Two hundred miles without pavement, and the people who like it that way.",
			"A county fair, a prize pumpkin and a judge who has seen it all.",
			"A town mends the same fence every Sunday. Nobody remembers why.",
			"A radio station run from a pickup truck, broadcasting to eleven farms.",
			"The road loops back, and the first town looks different the second time.",
		},
		{
			"A second summer, a longer route and a map drawn by a ten-year-old.",
			"Two towns face each other across a river, joined by one small ferry.",
			"Following a highway that was taken off the maps in 1962.",
			"A cattle crossing that stops traffic for an hour every day, on purpose.",
			"The back roads after dark: owls, porch lights and a bait shop that never closes.",
			"The finale drives every road of the series in a single day.",
		},
	},
	"Signals": {{
		"How a town keeps time, from the church tower to the atomic clock in a basement.",
		"The tide tables harbors still print by hand, and why they are always right.",
		"Bell ringers, bell founders and the physics of a perfect peal.",
		"Hundreds of satellites pass overhead tonight. A backyard antenna can hear them.",
		"The shortwave operators who still talk across oceans every night.",
		"River gauges, flood sirens and the quiet network that warns a valley.",
	}},
	"The Gilded Hour": {{
		"Three musicians, one borrowed van and a tour booked by someone who has never toured.",
		"The night before the first show, the harmony falls apart.",
		"Forty people in a bookshop, and the best show they will ever play.",
		"The van breaks down between two cities, and so does the friendship.",
		"A song written in a laundromat becomes the one everyone asks for.",
		"The last night of the tour, back where it started.",
	}},
	"Weather for Beginners": {
		{
			"What clouds are made of, and how to tell a rain cloud from a fluffy one.",
			"Where wind comes from, with a kite, a windsock and a very windy hill.",
			"Why it rains, and how to build a rain gauge from a jar.",
			"Ice patterns on windows, and how frost forms overnight.",
			"A cloud that comes down to visit: what fog is and why it disappears.",
			"Sunlight, raindrops and a garden hose make a rainbow on purpose.",
			"Count the seconds between the flash and the boom to find the storm.",
			"Why the evening sky turns orange, and red on some nights.",
		},
		{
			"Every snowflake has six sides. Catching them on a dark mitten.",
			"How ice grows inside a storm cloud, and how big a hailstone can get.",
			"Morning drops on spiderwebs, and where they come from.",
			"The difference between mist and fog, measured with a flashlight.",
			"Why a sea breeze blows in the afternoon and turns around at night.",
			"How to stay cool on the hottest days, with a thermometer and plenty of shade.",
			"Clear skies, the first stars of the evening and the weather that hides them.",
			"Why summer is warm and winter is cold, explained with a lamp and an orange.",
		},
	},
}

// episodeSummary is the synopsis for season si, episode ei (zero-based) of
// a show; an episode the table does not cover gets a line from its title.
func episodeSummary(show string, si, ei int, title string) string {
	if seasons, ok := episodeSummaries[show]; ok && si < len(seasons) && ei < len(seasons[si]) {
		return seasons[si][ei]
	}
	return fmt.Sprintf("%s: an episode of %s.", title, show)
}

type collectionSeed struct {
	Title  string
	Movies []int // indexes into movies
	Shows  []int
}

var collections = []collectionSeed{
	{"Sable Point Stories", []int{1, 2, 16, 10}, []int{0}},
	{"Family Night", []int{2, 11, 19, 22}, []int{7}},
	{"Documentaries", []int{7, 15, 22}, []int{4, 5}},
	{"Thornwall & Zenith", []int{3, 8, 13, 18}, []int{2}},
}

type playlistSeed struct {
	Title  string
	Kind   string // audio | video
	Albums []int  // audio: one track from each of these albums (first track)
	Movies []int
}

var playlists = []playlistSeed{
	{"Sunday Morning", "audio", []int{0, 2, 6, 14, 18, 16, 11}, nil},
	{"Road Trip", "audio", []int{12, 7, 1, 15, 5, 17}, nil},
	{"Late Shift", "audio", []int{19, 10, 13, 3, 8}, nil},
	{"Weekend Watchlist", "video", nil, []int{0, 5, 10, 21}},
	{"Rainy Day Films", "video", nil, []int{6, 16, 17, 23}},
}
