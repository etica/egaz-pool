## Open Source EGAZ Mining Pool PPLNS / SOLO

### index page

![index/miners page](/screenshots/01.png?raw=true "index/miners page")

* Support for HTTP and Stratum mining
* Detailed block stats with luck percentage and full reward
* Failover geth instances: geth high availability built in
* Separate stats for workers: can highlight timed-out workers so miners can perform maintenance of rigs
* JSON-API for stats
* New vue based UI
* Configured for Etica (EGAZ), but the pool build works with ethash/etchash coins with some modifications

### Building on Linux

Dependencies:

  * go >= 1.19
  * core-geth
  * redis-server >= 2.8.0
  * nodejs >= 4 LTS
  * nginx

### Install dependencies and tools

     sudo apt-get update && apt-get upgrade
     sudo apt-get install build-essential make git screen unzip curl nginx pkg-config tcl wget nmap rsync ipset -y
     sudo ipset create blacklist hash:ip

### Install GO     
     wget https://storage.googleapis.com/golang/go1.21.12.linux-amd64.tar.gz
     tar -xvf go1.21.12.linux-amd64.tar.gz
     rm go1.121.12.linux-amd64.tar.gz
     sudo mv go /usr/local
     
### Add GO to the path - edit the file and add the two exports to the end of it, then save and exit.
     nano ~/.profile
     
     export GOROOT=/usr/local/go
     export PATH=$GOPATH/bin:$GOROOT/bin:$PATH

     source ~/.profile
     go version     <--this should be replied with your go version
    
### Install Node.js
    > NOTE: at this point keep your nodejs version <= 19.x.
    curl -sL https://deb.nodesource.com/setup_14.x | sudo -E bash -
    sudo apt-get install nodejs -y
     

### Install redis-server
    wget http://download.redis.io/redis-stable.tar.gz
    tar xvzf redis-stable.tar.gz
    cd redis-stable
    make
    make test
    sudo make install
    sudo mkdir /etc/redis
    sudo cp ~/redis-stable/redis.conf /etc/redis
    sudo nano /etc/redis/redis.conf
	search the document for "supervised" remove the # at the front of the line and change auto to systemd
        supervised systemd
	continue searching the document for "dir " change it to /var/lib/redis
    	dir /var/lib/redis
	setup user, group, and directory
 		sudo adduser --system --group --no-create-home redis
		sudo mkdir /var/lib/redis
		sudo chown redis:redis /var/lib/redis
		sudo chmod 770 /var/lib/redis
  
### Setup the redis.service file
	nano /etc/systemd/system/redis.service
    Paste this in there:
	[Unit]
	Description=Redis In-Memory Data Store
	After=network.target

	[Service]
	User=redis
	Group=redis
	ExecStart=/usr/local/bin/redis-server /etc/redis/redis.conf
	ExecStop=/usr/local/bin/redis-cli shutdown
	Restart=always

	[Install]
	WantedBy=multi-user.target
   save and exit.  Then start the daemon like this:
	sudo systemctl enable redis.service
 	sudo systemctl start redis.service
	sudo systemctl status redis.service, or for more detail try
 	sudo journalctl -f -u redis.service
   
It is recommended to bind your DB address on 127.0.0.1 or on internal ip. Also, please set up the password for advanced security!!!
    
### Download, Install and Run core-geth   
   git clone https://github.com/etica/core-geth.git
   cd core-geth
   make geth
   sudo mv build/bin/geth /usr/local/bin/

   now you can generate an account and UTC keystore file if you need to

   geth --datadir ~/.etica account new

   make sure you do not lose the generated UTC keystore file (its in ~/.geth/keystore) and dont forget your password

### Create the geth.service file   
   sudo nano /etc/systemd/system/geth.service
   Copy the following example into the file and alter as needed.

        [Unit]
        description=geth
        After=network-online.target

        [Service]
        ExecStart=/usr/local/bin/geth --etica \
	    --miner.etherbase "0x183071e172aA5995738F79a762f09FDd83b7D75B" \
	    --unlock "0x183071e172aA5995738F79a762f09FDd83b7D75B" \
	    --password "PATH_TO_PASSWORD.TXT_FILE" \
	    --mine --cache 2048 \
	    --maxpendpeers 50 \
	    --maxpeers 50 \
	    --syncmode "full" \
	    --datadir "~/.etica/" \
	    --http --http.addr "127.0.0.1" \
            --nat "extip:YOUR_PUBLIC_IP" \
	    --http.port "8545" --port "30303" \
    	    --allow-insecure-unlock \
	    --rpc.allow-unprotected-txs  \
	    --http.api eth,net,web3,personal,miner,txpool \
	    --identity "YOUR_NODE_NAME" \
	    --bootnodes "enode://7f2d5370b11c604f348da0ce62ad21aafa32cf7136c94496dbf39bf261e6c317dea25e41dfc20894f89e30c4a4b1a76f52e3742fffd77c690f8d5e1c3ae1c2b4@62.72.177.101:30310","enode://923cfa4e5059cc217a5ef2da6543b6ec86dfb0fb8f3b9c9e843a0a1db4c21ba5d9d6c9f493f20bee3a4775f8f7657d68ba5a463586a3c3227af7cd127012a207@72.137.255.178:30314" \
	    --ethstats "YOUR_NODE_NAME:etica@stats.etica-stats.org"
            User=POOL_OPERATOR_USERNAME
            Restart=always
            RestartSec=3
            [Install]
            WantedBy=multi-user.target

***the capitalized stuff in the file needs to be adjusted to your specific settings.  POOL_OPERATOR_USERNAME is your login account name***
    
Then run core-geth by the following commands

    $ sudo systemctl enable geth.service
    $ sudo systemctl start geth.service

If you want to debug the node command

    $ sudo journalctl -f -u geth.service
    
### Firewall
   at minimum, the following ports must be open/forwarded for the pool (adjust as per your setup)
   80/443  http and/or https
   30303   geth communication
   8888    http proxy
   8008    stratum port

Clone & compile:
    
    git clone https://github.com/etica/egaz-pool.git
    cd egaz-pool
    go build


## Install Frontend

### Modify configuration file

     nano ~/open-etc-pool-friends/www/config/environment.js

Read thru the file and change as needed.

    ApiUrl: '//your-pool-domain/',
    HttpHost: 'http://your-pool-domain',
    StratumHost: 'your-pool-domain',
    PoolFee: '1%',
    Unit: 'EGAZ',
    PayoutThreshold: '0.5 EGAZ',
    



### Building Frontend

    cd www

Change <code>ApiUrl: '//example.net/'</code> in <code>www/config/environment.js</code> to match your domain name. Also don't forget to adjust other options.

Install deps
     sudo npm install -g ember-cli@2.18
     sudo npm install -g bower
     npm install
     bower install
     ember install ember-truth-helpers
     npm install jdenticon@2.1.0

Build.
     
     chmod +x build.sh
    ./build.sh
    
    
### Run Pool api.json
It is required to run pool by serviced. If it is not, the terminal could be stopped, and pool doesn’t work.

     sudo nano /etc/systemd/system/api.service

Copy the following example

```
[Unit]
Description=api
After=network-online.target

[Service]
ExecStart=/home/pool/open-etc-pool-friends/open-etc-pool-friends /home/pool/open-etc-pool-friends/api.json

User=pool

Restart=always
RestartSec=3

[Install]
WantedBy=multi-user.target
```
Then run api by the following commands

     sudo systemctl enable api
     sudo systemctl start api

If you want to debug the node command

     sudo systemctl status api

As you can see above, the frontend of the pool homepage is created. Then, move to the directory, www, which services the file.

Set up nginx.

     sudo nano /etc/nginx/sites-available/default

Modify based on configuration file.

    # Default server configuration
    # nginx example

    upstream api {
        server 127.0.0.1:8080;
    }

    server {
        listen 80 default_server;
        listen [::]:80 default_server;
        root /var/www/etc2pool;

        # Add index.php to the list if you are using PHP
        index index.html index.htm index.nginx-debian.html;

        server_name _;

        location / {
                # First attempt to serve request as file, then
                # as directory, then fall back to displaying a 404.
                try_files $uri $uri/ =404;
        }

        location /api {
                proxy_pass http://127.0.0.1:8080;
        }

    }

After setting nginx is completed, run the command below.

     sudo service nginx restart
     
Status all.

     sudo journalctl -f 
    
    
#### Customization

You can customize the layout using built-in web server with live reload:

    ember server --port 8082 --environment development

**Don't use built-in web server in production**.

Check out <code>www/app/templates</code> directory and edit these templates
in order to customise the frontend.

### Configuration

Configuration is actually simple, just read it twice and think twice before changing defaults.

**Don't copy config directly from this manual. Use the example config from the package,
otherwise you will get errors on start because of JSON comments.**

```javascript
{
  // Set to the number of CPU cores of your server
  "threads": 2,
  // Prefix for keys in redis store
  "coin": "etc",
  // Give unique name to each instance
  "name": "main",
  // shares or (solo "pplns": 0,)
  "pplns": 9000,
   // mordor, classic, ethereum, ropsten or ubiq, etica,
   //  ethereumPow, ethereumFair, expanse, octaspace, canxium, universal
  "network": "classic",
  // etchash, ethash, ubqhash
  "algo": "etchash",
  // exchange api coingecko
  "coin-name":"etc",
  
  "proxy": {
    "enabled": true,

    // Bind HTTP mining endpoint to this IP:PORT
    "listen": "0.0.0.0:8888",

    // Allow only this header and body size of HTTP request from miners
    "limitHeadersSize": 1024,
    "limitBodySize": 256,

    /* Set to true if you are behind CloudFlare (not recommended) or behind http-reverse
      proxy to enable IP detection from X-Forwarded-For header.
      Advanced users only. It's tricky to make it right and secure.
    */
    "behindReverseProxy": false,

    // Stratum mining endpoint
    "stratum": {
      "enabled": true,
      // Bind stratum mining socket to this IP:PORT
      "listen": "0.0.0.0:8008",
      "timeout": "120s",
      "maxConn": 8192,
      "tls": false,
      "certFile": "/path/to/cert.pem",
      "keyFile": "/path/to/key.pem"
    },

    // Try to get new job from node in this interval
    "blockRefreshInterval": "120ms",
    "stateUpdateInterval": "3s",
    // Require this share difficulty from miners
    "difficulty": 2000000000,

    /* Reply error to miner instead of job if redis is unavailable.
      Should save electricity to miners if pool is sick and they didn't set up failovers.
    */
    "healthCheck": true,
    // Mark pool sick after this number of redis failures.
    "maxFails": 100,
    // TTL for workers stats, usually should be equal to large hashrate window from API section
    "hashrateExpiration": "3h",

    "policy": {
      "workers": 8,
      "resetInterval": "60m",
      "refreshInterval": "1m",
      //blacklist Wallet for miners
      "blacklist_file" : "/home/pool/open-etc-pool-friends/stratum_blacklist.json",

      "banning": {
        "enabled": false,
        /* Name of ipset for banning.
        Check http://ipset.netfilter.org/ documentation.
        */
        "ipset": "blacklist",
        // Remove ban after this amount of time
        "timeout": 1800,
        // Percent of invalid shares from all shares to ban miner
        "invalidPercent": 30,
        // Check after after miner submitted this number of shares
        "checkThreshold": 30,
        // Bad miner after this number of malformed requests
        "malformedLimit": 5
      },
      // Connection rate limit
      "limits": {
        "enabled": false,
        // Number of initial connections
        "limit": 30,
        "grace": "5m",
        // Increase allowed number of connections on each valid share
        "limitJump": 10
      }
    }
  },

  // Provides JSON data for frontend which is static website
  "api": {
    "enabled": true,
    "listen": "0.0.0.0:8080",
    // Collect miners stats (hashrate, ...) in this interval
    "statsCollectInterval": "5s",
    // Purge stale stats interval
    "purgeInterval": "10m",
    // Fast hashrate estimation window for each miner from it's shares
    "hashrateWindow": "30m",
    // Long and precise hashrate from shares, 3h is cool, keep it
    "hashrateLargeWindow": "3h",
    // Collect stats for shares/diff ratio for this number of blocks
    "luckWindow": [64, 128, 256],
    // Max number of payments to display in frontend
    "payments": 50,
    // Max numbers of blocks to display in frontend
    "blocks": 50,

    /* If you are running API node on a different server where this module
      is reading data from redis writeable slave, you must run an api instance with this option enabled in order to purge hashrate stats from main redis node.
      Only redis writeable slave will work properly if you are distributing using redis slaves.
      Very advanced. Usually all modules should share same redis instance.
    */
    "purgeOnly": false
  },

  // Check health of each node in this interval
  "upstreamCheckInterval": "5s",

  /* List of parity nodes to poll for new jobs. Pool will try to get work from
    first alive one and check in background for failed to back up.
    Current block template of the pool is always cached in RAM indeed.
  */
  "upstream": [
    {
      "name": "main",
      "url": "http://127.0.0.1:8545",
      "timeout": "10s"
    },
    {
      "name": "backup",
      "url": "http://127.0.0.2:8545",
      "timeout": "10s"
    }
  ],

  // This is standard redis connection options
  "redis": {
    // Where your redis instance is listening for commands
    "endpoint": "127.0.0.1:6379",
    "poolSize": 10,
    "database": 0,
    "password": "",
        "sentinelEnabled": false,
        "masterName": "mymaster",
        "sentinelAddrs": [
            "127.0.0.1:26379",
            "127.0.0.1:26389",
            "127.0.0.1:26399"
        ]
	},

  "exchange": {
    "enabled": true,
     "url": "https://api.coingecko.com/api/v3/coins/markets?vs_currency=usd&ids=ethereum-classic",
     "timeout": "50s",
     "refreshInterval": "900s"
    },

  // This module periodically remits ether to miners
  "unlocker": {
    "enabled": false,
    // Pool fee percentage
    "poolFee": 1.0,
    // Pool fees beneficiary address (leave it blank to disable fee withdrawals)
    "poolFeeAddress": "",
    // Donate 10% from pool fees to developers
    "donate": true,
    // Unlock only if this number of blocks mined back
    "depth": 120,
    // Simply don't touch this option
    "immatureDepth": 20,
    // Keep mined transaction fees as pool fees
    "keepTxFees": false,
    // Run unlocker in this interval
    "interval": "10m",
    // Parity node rpc endpoint for unlocking blocks
    "daemon": "http://127.0.0.1:8545",
    // Rise error if can't reach parity
    "timeout": "10s",
    // london hard fork ON or OFF
    "isLondonHardForkEnabled": false
  },

  // Pay out miners using this module
  "payouts": {
    "enabled": false,
    // Require minimum number of peers on node
    "requirePeers": 25,
    // Run payouts in this interval
    "interval": "12h",
    // Parity node rpc endpoint for payouts processing
    "daemon": "http://127.0.0.1:8545",
    // Rise error if can't reach parity
    "timeout": "10s",
    // Address with pool balance
    "address": "0x0",
    // Let parity to determine gas and gasPrice
    "autoGas": true,
    // Sends as EIP1559 TX
    "maxPriorityFee": "2000000000",
    // Gas amount and price for payout tx (advanced users only)
    "gas": "21000",
    "gasPrice": "50000000000",
    // Send payment only if miner's balance is >= 0.5 Ether
    "threshold": 500000000,
    // Perform BGSAVE on Redis after successful payouts session
    "bgsave": false
  }
}
```

If you are distributing your pool deployment to several servers or processes,
create several configs and disable unneeded modules on each server. (Advanced users)

I recommend this deployment strategy:

* Mining instance - 1x (it depends, you can run one node for EU, one for US, one for Asia)
* Unlocker and payouts instance - 1x each (strict!)
* API instance - 1x

### Notes

* Unlocking and payouts are sequential, 1st tx go, 2nd waiting for 1st to confirm and so on. You can disable that in code. Carefully read `docs/PAYOUTS.md`.
* Also, keep in mind that **unlocking and payouts will halt in case of backend or node RPC errors**. In that case check everything and restart.
* You must restart module if you see errors with the word *suspended*.
* Don't run payouts and unlocker modules as part of mining node. Create separate configs for both, launch independently and make sure you have a single instance of each module running.
* If `poolFeeAddress` is not specified all pool profit will remain on coinbase address. If it specified, make sure to periodically send some dust back required for payments.

### Mordor

To use this pool on the mordor testnet two settings require changing to "mordor"

network in your config.json (this sets backend (validation,unlocker) to mordor paramaters)
APP.Network in your www/config/environment.js (this sets the frontend to mordor paramaters)
rerun ./build.sh


### Extra) How To Secure the pool frontend with Let's Encrypt (https)

First, install the Certbot's Nginx package with apt-get

```
 sudo apt-get update
 sudo apt-get install python3-certbot-nginx
```

And then open your nginx setting file, make sure the server name is configured!

```
 sudo nano /etc/nginx/sites-available/default
. . .
server_name <your-pool-domain>;
. . .
```

Change the _ to your pool domain, and now you can obtain your auto-renewaled ssl certificate for free!

```
 sudo certbot --nginx -d <your-pool-domain>
```

Now you can access your pool's frontend via https! Share your pool link!


