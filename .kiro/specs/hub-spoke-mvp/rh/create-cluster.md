Create the cluster
For the purposes of this learning path, make the following selections.

Cluster settings
Details:

Cluster name: <pick a name>
Version: <select latest version>
Region: <select desired region>
Availability: Single zone
Enable user workload monitoring: leave checked
Enable additional etcd encryption: leave unchecked
Encrypt persistent volumes with customer keys: leave unchecked
Click "Next".

Machine pool (leave the defaults which are):

Compute node instance type: m5.xlarge - 4 vCPU 16 GiB RAM
Enable autoscaling: unchecked
Compute node count: 2
Leave node labels blank
Click "Next".

Networking

Configuration - Leave all default values

Click "Next".

CIDR ranges - Leave all default values

Click "Next".

Cluster roles and policies

For the purposes of this workshop leave "Auto" selected and it will make the cluster deployment process simpler and quicker.

NOTE: If you selected a Basic OCM role earlier you can only use manual mode and you must manually create the operator roles and OIDC provider. See "For Basic OCM roles only" section below after you've completed the "Cluster updates" section and started the cluster creation.

Cluster updates

Leave all the default options.

Review and create

Review the content for the cluster configuration and click "Create cluster".