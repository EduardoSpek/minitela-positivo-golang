export namespace main {
	
	export class noteRule {
	    text: string;
	    mode: string;
	    onceAt: string;
	    time: string;
	    weekdayBit: number;
	    fired: boolean;
	
	    static createFrom(source: any = {}) {
	        return new noteRule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.text = source["text"];
	        this.mode = source["mode"];
	        this.onceAt = source["onceAt"];
	        this.time = source["time"];
	        this.weekdayBit = source["weekdayBit"];
	        this.fired = source["fired"];
	    }
	}
	export class scheduleRule {
	    enabled: boolean;
	    start: string;
	    end: string;
	    page: number;
	    brightness: number;
	
	    static createFrom(source: any = {}) {
	        return new scheduleRule(source);
	    }
	
	    constructor(source: any = {}) {
	        if ('string' === typeof source) source = JSON.parse(source);
	        this.enabled = source["enabled"];
	        this.start = source["start"];
	        this.end = source["end"];
	        this.page = source["page"];
	        this.brightness = source["brightness"];
	    }
	}

}

