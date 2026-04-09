package seed

// Word lists used to synthesize human-plausible names, cities, categories,
// and so on. Kept short on purpose — the generator combines them, so even a
// few hundred tokens produce millions of unique values.

var firstNames = []string{
	"Ada", "Alex", "Ahmed", "Amelia", "Anders", "Ari", "Asha", "Aubrey",
	"Ben", "Bianca", "Brooke", "Bryce", "Camila", "Cara", "Casey", "Cedric",
	"Chen", "Clara", "Cole", "Daisy", "Dante", "Dara", "Dash", "Dawn",
	"Diego", "Dmitri", "Duc", "Eden", "Eli", "Elena", "Elias", "Ember",
	"Emil", "Emma", "Enzo", "Esme", "Eva", "Evan", "Faith", "Farah",
	"Finn", "Fiona", "Felix", "Gabe", "Gia", "Gillian", "Grant", "Greta",
	"Hal", "Hana", "Henry", "Holly", "Huck", "Ida", "Ivy", "Iris",
	"Jade", "Jamal", "Jana", "Jasper", "Jett", "Jo", "Joelle", "Jonah",
	"June", "Kai", "Kara", "Keaton", "Keira", "Kenji", "Kiera", "Kiran",
	"Kyra", "Lana", "Lars", "Layla", "Leah", "Leo", "Levi", "Liam",
	"Lila", "Lina", "Livia", "Logan", "Lucia", "Luna", "Mac", "Maeve",
	"Maia", "Marco", "Mari", "Mateo", "Maya", "Mei", "Milo", "Mira",
	"Nadia", "Nash", "Nate", "Nia", "Nika", "Nikhil", "Nina", "Noa",
	"Noah", "Nora", "Odin", "Olive", "Omar", "Orla", "Owen", "Paige",
	"Pax", "Penn", "Pia", "Pierce", "Priya", "Quinn", "Rafa", "Rain",
	"Raul", "Ravi", "Reed", "Remi", "Rhea", "Rio", "River", "Roman",
	"Rosa", "Ruby", "Rudy", "Sage", "Saira", "Sam", "Sana", "Sascha",
	"Scout", "Seren", "Shay", "Shira", "Sid", "Simone", "Sky", "Soren",
	"Stella", "Sven", "Tara", "Tate", "Teo", "Theo", "Thora", "Tomas",
	"Tova", "Uma", "Vada", "Vera", "Viv", "Wade", "Wren", "Xan",
	"Xiu", "Yara", "Yoko", "Yusuf", "Zain", "Zara", "Zephyr", "Zoe",
}

var lastNames = []string{
	"Anderson", "Ayala", "Baker", "Banda", "Bell", "Berger", "Bhatt",
	"Blake", "Bloom", "Brady", "Brennan", "Brown", "Cabrera", "Calder",
	"Callahan", "Carter", "Castillo", "Chen", "Chu", "Clarke", "Cohen",
	"Cole", "Collins", "Connor", "Cortez", "Costa", "Cruz", "Dalton",
	"Daniels", "Davies", "Davis", "Delgado", "Diaz", "Duarte", "Dunn",
	"Eaton", "Edwards", "Eldridge", "Ellis", "Espino", "Evans", "Faber",
	"Falk", "Farley", "Farmer", "Ferris", "Finch", "Fisher", "Fleming",
	"Flores", "Foley", "Ford", "Fox", "Francis", "Frazier", "Frost",
	"Gallagher", "Garcia", "Gates", "Gibbs", "Gibson", "Gill", "Glover",
	"Gomez", "Grant", "Green", "Griffin", "Gupta", "Hale", "Hall",
	"Hamilton", "Hansen", "Hardy", "Harper", "Harris", "Hart", "Hayes",
	"Hendricks", "Hensley", "Herrera", "Higgins", "Hill", "Hogan",
	"Holmes", "Hooper", "Hopper", "Howard", "Hsu", "Huang", "Hudson",
	"Hughes", "Hunt", "Ibarra", "Iqbal", "Irwin", "Jackson", "Jacobs",
	"James", "Jennings", "Jensen", "Jimenez", "Johnson", "Jones", "Kane",
	"Kaur", "Keller", "Kelly", "Kennedy", "Khan", "Kim", "King",
	"Klein", "Knight", "Knox", "Koch", "Krause", "Kumar", "Lam", "Lane",
	"Larson", "Lawson", "Le", "Lee", "Leung", "Lewis", "Li", "Lin",
	"Lloyd", "Long", "Lopez", "Lowe", "Lucas", "Lund", "Lynch",
	"Mackenzie", "Madden", "Mahmoud", "Marks", "Marsh", "Martin",
	"Martinez", "Matthews", "Maxwell", "May", "Mayer", "McAllister",
	"McBride", "McCarthy", "McCloud", "Medina", "Mehta", "Mendez", "Meyer",
	"Miller", "Mills", "Miranda", "Mitchell", "Molina", "Moore", "Morales",
	"Moreno", "Morgan", "Morris", "Murphy", "Murray", "Navarro", "Nelson",
	"Newton", "Nguyen", "Nichols", "Nolan", "Norris", "Novak", "Oakes",
	"Obrien", "Ochoa", "Odell", "Okafor", "Olsen", "Olson", "Ortega",
	"Osborne", "Owens", "Pace", "Page", "Palmer", "Park", "Parker",
	"Parsons", "Patel", "Patterson", "Payne", "Pearce", "Perez", "Perry",
	"Peters", "Pham", "Phan", "Phillips", "Pierce", "Pitts", "Porter",
	"Powell", "Powers", "Prasad", "Price", "Quinn", "Ramirez", "Ramos",
	"Randall", "Rao", "Reed", "Reese", "Reid", "Reyes", "Reynolds",
	"Rhodes", "Rice", "Richards", "Riley", "Rivera", "Roberts", "Robinson",
	"Rodriguez", "Rogers", "Rojas", "Romano", "Romero", "Rose", "Ross",
	"Rowe", "Russell", "Ryan", "Sanchez", "Sanders", "Santos", "Savage",
	"Schmidt", "Schneider", "Schultz", "Scott", "Sharp", "Shaw", "Sherman",
	"Short", "Silva", "Simmons", "Singh", "Sinha", "Sloan", "Smith",
	"Snyder", "Solis", "Sosa", "Soto", "Stewart", "Stokes", "Stone",
	"Strickland", "Suarez", "Sullivan", "Sun", "Sutton", "Tan", "Tanaka",
	"Taylor", "Terry", "Thomas", "Thompson", "Todd", "Torres", "Tran",
	"Trejo", "Tucker", "Turner", "Underwood", "Vance", "Vargas", "Vasquez",
	"Vaughn", "Vega", "Vogel", "Wade", "Walker", "Walsh", "Wang", "Ward",
	"Warner", "Watson", "Weaver", "Webb", "Weber", "Wells", "West",
	"Wheeler", "White", "Williams", "Wilson", "Wise", "Wolfe", "Wong",
	"Wood", "Wright", "Wu", "Xu", "Yang", "Yates", "Yoon", "Young",
	"Yu", "Zhang", "Zhao", "Ziegler", "Zimmerman",
}

var cities = []string{
	"Akron", "Albany", "Albuquerque", "Amsterdam", "Antwerp", "Athens",
	"Atlanta", "Auckland", "Austin", "Baltimore", "Bangalore", "Bangkok",
	"Barcelona", "Belfast", "Berlin", "Birmingham", "Bogota", "Boise",
	"Bologna", "Boston", "Boulder", "Brighton", "Brisbane", "Brussels",
	"Bucharest", "Budapest", "Buenos Aires", "Cairo", "Calgary", "Cape Town",
	"Caracas", "Cardiff", "Charlotte", "Chicago", "Cincinnati", "Cleveland",
	"Cologne", "Columbus", "Copenhagen", "Dallas", "Delhi", "Denver",
	"Detroit", "Dubai", "Dublin", "Durban", "Edinburgh", "Edmonton",
	"El Paso", "Fargo", "Frankfurt", "Fresno", "Geneva", "Glasgow",
	"Gothenburg", "Guadalajara", "Hamburg", "Hanoi", "Helsinki", "Honolulu",
	"Houston", "Indianapolis", "Istanbul", "Izmir", "Jakarta", "Jerusalem",
	"Johannesburg", "Kansas City", "Karachi", "Kiev", "Kolkata", "Kraków",
	"Kyoto", "Lagos", "Las Vegas", "Leeds", "Lima", "Lisbon", "Liverpool",
	"London", "Los Angeles", "Louisville", "Lyon", "Madrid", "Manchester",
	"Manila", "Marseille", "Medellin", "Melbourne", "Memphis", "Mexico City",
	"Miami", "Milan", "Milwaukee", "Minneapolis", "Minsk", "Montevideo",
	"Montreal", "Moscow", "Mumbai", "Munich", "Nashville", "New Orleans",
	"New York", "Newcastle", "Nice", "Orlando", "Osaka", "Oslo", "Ottawa",
	"Palermo", "Paris", "Perth", "Philadelphia", "Phoenix", "Pittsburgh",
	"Portland", "Porto", "Prague", "Quito", "Raleigh", "Reykjavik",
	"Richmond", "Rio de Janeiro", "Riyadh", "Rome", "Rotterdam", "Sacramento",
	"Salem", "Salt Lake City", "San Diego", "San Francisco", "San Jose",
	"Sao Paulo", "Sapporo", "Seattle", "Seoul", "Seville", "Shanghai",
	"Singapore", "Sofia", "St. Louis", "Stockholm", "Stuttgart", "Sydney",
	"Taipei", "Tampa", "Tashkent", "Tbilisi", "Tehran", "Tokyo", "Toronto",
	"Toulouse", "Turin", "Utrecht", "Valencia", "Vancouver", "Venice",
	"Vienna", "Vilnius", "Warsaw", "Washington", "Wellington", "Zurich",
}

var countries = []string{
	"Argentina", "Australia", "Austria", "Belgium", "Brazil", "Canada",
	"Chile", "China", "Colombia", "Czechia", "Denmark", "Egypt", "Finland",
	"France", "Germany", "Greece", "Hungary", "Iceland", "India", "Indonesia",
	"Ireland", "Israel", "Italy", "Japan", "Kenya", "Mexico", "Morocco",
	"Netherlands", "New Zealand", "Nigeria", "Norway", "Pakistan", "Peru",
	"Philippines", "Poland", "Portugal", "Romania", "Russia", "Saudi Arabia",
	"Singapore", "South Africa", "South Korea", "Spain", "Sweden",
	"Switzerland", "Thailand", "Turkey", "UAE", "Ukraine", "United Kingdom",
	"United States", "Uruguay", "Vietnam",
}

var departmentNames = []string{
	"Engineering", "Sales", "Marketing", "Finance", "Human Resources",
	"Operations", "Customer Support", "Legal", "Research", "Logistics",
}

var productCategories = []string{
	"Widgets", "Gadgets", "Fasteners", "Connectors", "Sensors", "Controllers",
	"Enclosures", "Cables", "Power Supplies", "Tools", "Safety Gear",
	"Consumables",
}

var productAdjectives = []string{
	"Standard", "Premium", "Industrial", "Heavy-Duty", "Compact", "Mini",
	"Pro", "Ultra", "Rapid", "Silent", "Modular", "Universal", "Deluxe",
	"Smart", "Connected", "Ruggedized", "Precision", "Titanium", "Ceramic",
	"Copper", "Aluminum", "Graphene", "Quantum", "Eco", "HyperFlex",
	"OmniGrip", "TurboCore", "NovaSeal", "ArcticPulse", "SolarFlare",
}

var productNouns = []string{
	"Bracket", "Clamp", "Flange", "Gear", "Bearing", "Coupling", "Housing",
	"Valve", "Hinge", "Spring", "Pulley", "Shaft", "Rotor", "Stator",
	"Wrench", "Driver", "Ratchet", "Gauge", "Meter", "Relay", "Switch",
	"Breaker", "Inverter", "Regulator", "Actuator", "Solenoid", "Pump",
	"Fan", "Blower", "Compressor", "Harness", "Connector", "Adapter",
	"Coupler", "Plug", "Receptacle",
}

var supplierWords = []string{
	"Apex", "Northstar", "Ironworks", "Pinnacle", "Vanguard", "Summit",
	"Beacon", "Keystone", "Corebridge", "Forge", "Helix", "Lumen",
	"Meridian", "Polaris", "Stratos", "Titan", "Tribeca", "Axiom",
	"Crestline", "Delta", "Equinox", "Falcon", "Gravitas", "Horizon",
	"Indigo", "Jetstream", "Kismet", "Lantern", "Marquis", "Nebula",
	"Opal", "Phoenix", "Quanta", "Redwood", "Silvermark", "Thorn",
	"Umbra", "Vertex", "Wexler", "Xylo", "Yonder", "Zenith",
}

var supplierSuffixes = []string{
	"Industries", "Supply Co", "Systems", "Works", "Manufacturing",
	"Holdings", "Group", "Solutions", "Components", "Trading", "Tech",
	"Partners", "Logistics", "Materials", "Metalworks",
}

var orderStatuses = []string{
	"pending", "confirmed", "picking", "packed", "shipped", "delivered",
	"cancelled", "returned",
}

var userRoles = []string{
	"admin", "manager", "analyst", "engineer", "support", "sales",
	"readonly", "auditor",
}
