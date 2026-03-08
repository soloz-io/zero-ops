[Music] 
 In this video, I'm going to show you how 
 to solve a common problem when you're 
 trying to create a multi-user MCP server 
 for an existing backend API. 
 The backend API might be authenticated 
 using an API token or in this case O. 
 I've got one identity provider that's 
 authenticating the backend service and a 
 different identity provider that's being 
 used for the agentic part the MCP server 
 and the agentic client that I'm using 
 goose my backend service is an order 
 status service 
 I have built an MCP server for it and 
 then I'm using goose as an agentic 
 client goose has the ability to create 
 recipes which are just pre-anned ways of 
 interacting with your agent so let's go 
 ahead and start the agent. 
 Goose automatically loads the order 
 status MCP tool for us and gives me a 
 sample prompt. So, I'm going to go ahead 
 and send that prompt to the agent. 
 The agent calls the tool 
 and tells me that the call from the MCP 
 tool to the backend service is not 
 authenticated and I need to visit a URL 
 to enable the MCP server to access the 
 backend service. 
 I put that into my web browser and I 
 need to log in here. I'm logging logging 
 into the agentic side. So this is key 
 click used for authentication. 
 Now I'm going to be logging in as an 
 administrative user. So I'm going to see 
 the entire solo enterprise for agent 
 gateway dashboard. This includes things 
 like the ability to show your gateways, 
 your routes, your destinations and 
 policies as well as a playground. And of 
 course the all important list of 
 elicitations that we're going to use in 
 just a moment. 
 On the dashboard for Solo Enterprise for 
 Agent Gateway, we've got a number of 
 useful charts and metrics that show you 
 how the system is performing. 
 Now, let's proceed to authorize the 
 elicitation that's been created for us 
 when we tried to call the backend 
 service through the tool. This takes me 
 across to or zero. This is the IDP 
 that's being used to protect that 
 backend service. This is a fairly common 
 scenario when you have many services 
 built over a number of years. 
 or being used from external providers. 
 The identity domain for each is 
 different. As you can see, I have a 
 totally different identity that I'm 
 logging into this IDP with. Now, the 
 dashboard tells me that the elicitation 
 is complete. That means that agent 
 gateway now has the correct token for 
 this user to log in to that backend 
 service. If I logged in as a different 
 user to agent gateway, I would see a 
 different list of elicitations. So now 
 we're back in the client and I tell 
 Goose that I'm authenticated now. So it 
 should try the request again. As you can 
 see, it goes off. It calls the order 
 status service and this time it returns 
 the order status. 
 Now let's take a quick look at the 
 architecture. 
 As I showed in the demo, my user is 
 using goose. Goose has a token from the 
 internal IDP keycloak that it uses to 
 send to the tool. That tool is protected 
 by agent gateway. Agent gateway acts as 
 the ability to control access to tools 
 based on policy. Agent gateway can also 
 act as a virtual tool server where it 
 can multiplex multiple different MCP 
 servers and tools into a single 
 endpoint. 
 The call then proceeds to the tool. The 
 tool needs now to call the backend 
 service. It does this again via agent 
 gateway. 
 Agent gateway detects that we don't have 
 the right token to call the backend 
 service and so it returns the message 
 about requiring the user to get the 
 token through the tool and back to the 
 client. The user then visits the portal, 
 completes the token workflow and tells 
 the 
 client to continue. The client then goes 
 back through the agent gateway in the 
 tool 
 to make the call to the backend service. 
 This time when the call goes through the 
 agent gateway, it detects that it does 
 indeed have the right token stored for 
 accessing the backend service and is 
 able to swap the tokens. Now the call 
 goes through and out of this domain and 
 into a different domain of identity, but 
 this time with the correct token. 
 It's able to call the backend service, 
 validate the token against the external 
 IDP, and return the result to the user. 
 Finally, let's take a quick look at some 
 of the code. Here, I've got an MCP 
 server. I built it using KMCP, a tool 
 that we built to make it easy to run MCP 
 servers in Kubernetes. And I built the 
 tool using fast MCP. I added a couple of 
 simple tools that I was just using for 
 debugging the service. And then I added 
 the order status call. It's really 
 simple. All it does is the request 
 through to the back end. 
 The back end then responds with the 
 relevant information about the order to 
 and passes this back as an MCP call to 
 the client. Of course, in reality, your 
 MCP server would be considerably more 
 complicated than this one. 
 So to recap, in this video, I've shown 
 you how you can use Solo Enterprise for 
 agent gateway to solve a common problem 
 you have when building 
 MCP servers. How do you authenticate the 
 call from the MCP server to the back end 
 for a multi-user MCP server? 
 [Music]